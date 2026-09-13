---
title: Debugging kubeswap backends when a model fails to load
summary: The failure chain from kubeswap status reasons to backend pod logs, and what each symptom (Pending, CrashLoopBackOff, never Ready) usually means.
category: guides
tags: [kubeswap, kubernetes, debugging, logs, pod, gpu, pending, crashloopbackoff, ready, troubleshooting]
config_keys: [models.*.cmd, models.*.cmdStop, healthCheckTimeout, unloadTimeout]
updated: 2026-09-12
---

# Debugging kubeswap backends when a model fails to load

The wrapper's job is to make the backend pod Ready; when it does not, the
diagnosis walks down one chain, each step narrowing the suspect:

```
kubeswap status      →  which model, and WHY (REASON column)
kubeswap logs        →  the engine's own output (load errors live here)
kubectl describe pod →  scheduler verdict, events, volume errors
kubectl logs         →  same as kubeswap logs, for when the wrapper is not
                        the one you want to ask (or you are already in kubectl)
```

```bash
kubeswap status --namespace llama-swap
kubeswap logs --model lfm25-230m --namespace llama-swap --tail 200
```

`kubeswap logs` finds the model's pod by its deployment label, so models
whose IDs sanitize to the same string cannot be mixed up. The head-end's own
logs carry the wrapper's log forwarding (prefixed `[pod/<name>]`) as a
third view of the same backend output; if RBAC refused the log stream,
forwarding is disabled once with a warning there.

## Symptoms

| symptom (status REASON / pod state) | usual cause | fix |
| --- | --- | --- |
| `unscheduled: 0/N nodes are available: N Insufficient amd.com/gpu` | GPU requested but none free, or the resource name is wrong for this cluster | free a GPU, or correct `--gpu <resource>=1` to the name `kubectl describe node` shows under `Capacity` |
| `unscheduled` with a taint message | node has a taint the pod does not tolerate | add `--toleration key:Equal::NoSchedule` (format `key:operator:value:effect`) |
| `Pending`, stuck in `ContainerCreating` | volume the node cannot mount (RWO PVC on the wrong node, missing claim) | check the storage mode against where the pod can land (see the storage article); kubeswap also rejects a read-write mount on a read-only PVC at `serve` time |
| `CrashLoopBackOff` | the engine died while loading | `kubeswap logs` — bad model path, OOM, missing Vulkan/CUDA libraries, wrong flags |
| Running, never Ready | server bound to 127.0.0.1 (missing `--host 0.0.0.0` / `--listen-ip 0.0.0.0`), wrong `--health-path`, or load outlasting the startup probe | check the probe path in `kubectl describe pod`; raise `--startup-timeout` for slow loads and keep `healthCheckTimeout` at least that long |
| `ImagePullBackOff` | wrong tag, private registry, no pull secret | `kubectl describe pod` shows the pull error |
| Ready but requests hang | a wedged GPU: the engine answers `/health` but inference stalls | nothing in-cluster will reset it; kill the pod (`kubeswap delete` then re-request) |

Two semantics that catch people out:

- **A failing model keeps restarting by design.** The startup probe restarts
  the pod until the engine answers, so a transient storage or image hiccup
  recovers on its own — but a bad model path restarts forever. Read the logs
  rather than waiting it out.
- **Adoption keeps the old spec.** `kubeswap serve` adopts an existing
  deployment unless `--strict` is set. To apply a config fix to a model that
  is already loaded (or already failing), `kubeswap delete --model <id>`
  first (or run serve with `--strict`), then let it reload with the new
  command line.

## When the head-end is the suspect

The head-end's readiness/liveness probes hit llama-swap's own `/health`,
which is unconditional while the process is alive — a slow model load can
never crash-loop the head-end. If the head-end pod itself restarts, look at
its logs (config parse, RBAC 403s against the namespace, a config reload
loop) rather than at the backends.
