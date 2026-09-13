---
title: Choosing storage for model weights in a kubeswap namespace
summary: Why the shared model-cache PVC wants ReadWriteMany, which drivers provide it, and the RWO / emptyDir escapes when it does not.
category: guides
tags: [kubeswap, kubernetes, pvc, rwx, storage, longhorn, ceph, efs, azure-files, model-cache, node-selector]
updated: 2026-09-12
---

# Choosing storage for model weights in a kubeswap namespace

Models are loaded on demand and evicted by TTL or swap-in: a model's pod is
deleted today and recreated on the *next* request, possibly on a **different
node**. A shared model-cache PVC must therefore be mountable on every node a
backend can land on — in practice that means `ReadWriteMany`. With a
`ReadWriteOnce` claim, an evicted model can reschedule onto a node that
cannot mount the volume, and the pod sits in `ContainerCreating` with a
volume error while the router waits out `healthCheckTimeout`.

## Drivers that provide RWX

| driver | notes |
| --- | --- |
| Longhorn | the default in the examples; RWX via its NFS/replica mode, homelab-friendly |
| CephFS (Rook) | RWX; the natural choice when the cluster already runs Rook |
| Azure Files | RWX; the managed option on AKS |
| AWS EFS | RWX; the managed option on EKS |
| NFS server | RWX via the NFS CSI driver or a static PV |

Cloud block storage (EBS, PD, managed disks) is `ReadWriteOnce` only — do not
reach for it for a shared cache.

GPU is not a requirement anywhere in this setup: the head-end needs no GPU,
and a model without `--gpu` gets a plain CPU pod that schedules on any node
(the whisper and TTS models in the examples article run exactly that way).
The storage rules are the same for CPU-only fleets — a model evicted and
reloaded later can land on any node, so a cache shared across several CPU
nodes wants RWX just as much as a GPU one. A single-node CPU setup can use
RWO or a local volume with the pin below.

## The RWO escape: pin the pods to one node

If the cluster has a single GPU node (the common homelab shape), RWO block
storage works fine **as long as every backend lands there** — pin them:

```yaml
cmd: >-
  kubeswap serve
  --listen 127.0.0.1:${PORT}
  --model lfm25-230m
  --namespace llama-swap
  --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
  --gpu amd.com/gpu=1
  --node-selector kubernetes.io/hostname=gpu-node      # every backend, same node
  --volume pvc:llama-swap-models:/models:ro
  -- --model /models/model.gguf --port 8080
```

With the pin in place, a `ReadWriteOnce` PVC (EBS, PD, Longhorn RWO) is
mountable wherever the pod runs. Drop the pin and you are back to needing
RWX.

## The zero-storage escape: emptyDir + pull

Small models can skip the PVC entirely: each pod downloads its weights into
an `emptyDir` on first load (the chart's default demo does exactly this with
`-hf repo:file`), and pays the download on every reload. Fine for a
135MB demo model, painful for 16GB image models.

```
--volume emptydir:model-cache:/models
-- --model /models/SmolLM2-135M-Instruct-Q4_0.gguf
   -hf QuantFactory/SmolLM2-135M-Instruct-GGUF:Q4_0
```

## How kubeswap treats the PVC

- A **missing** PVC is created by `kubeswap serve` with `--pvc-size`
  (default 1Gi), `--pvc-class` (cluster default) and `--pvc-access-mode`
  (`rwo` or `rwx`) — pass `--pvc-access-mode rwx` when you expect the cache
  to move between nodes.
- A **pre-existing** PVC is adopted as-is and never relabeled; declare it
  under the chart's `extraResources` so helm tracks it. A read-write mount
  (`--volume pvc:name:/path`) on a claim whose access modes cannot serve it
  (e.g. a read-only claim) fails `kubeswap serve` with an error naming the
  claim and the fix instead of leaving the pod stuck.
- `:ro` mounts work with any access mode — mount the shared cache read-only
  and keep scratch/slot state on `emptyDir`.

```yaml
# extraResources in the chart values — the idiomatic home for the cache PVC
extraResources:
  - apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: llama-swap-models
    spec:
      accessModes: [ReadWriteMany]
      storageClassName: longhorn
      resources:
        requests:
          storage: 35Gi
```
