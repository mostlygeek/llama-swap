---
title: Running inference servers in Kubernetes with kubeswap
summary: Use the kubeswap wrapper as cmd/cmdStop so llama-swap starts and stops backend servers as Kubernetes workloads in a dedicated namespace.
category: guides
tags: [kubernetes, kubeswap, deployment, k8s, operator, pod, namespace, pvc, gpu, multi-backend, sd-server, whisper-server, audiocpp_server]
config_keys: [models.*.cmd, models.*.cmdStop, models.*.proxy, models.*.checkEndpoint, models.*.capabilities, healthCheckTimeout, unloadTimeout]
updated: 2026-09-12
---

# Running inference servers in Kubernetes with kubeswap

`kubeswap` is a small wrapper binary shipped with llama-swap. It stands in
where `docker` would stand in a model's `cmd`/`cmdStop`: instead of starting
a local process, `kubeswap serve` creates (or adopts) a Deployment that runs
the inference server in a dedicated namespace, proxies to it on `${PORT}`,
and `kubeswap delete` tears it down. The llama-swap core is unchanged — a
Kubernetes model is just a model whose commands happen to talk to the
Kubernetes API.

## How it fits

- **Head end**: llama-swap (with `kubeswap` on its PATH) runs as a Deployment
  with a mounted `config.yaml` (`-watch-config` enabled) and a ServiceAccount
  with namespaced RBAC (deployments, services, PVCs, pods, pods/log).
- **Backends**: one pod per model, labeled `llama-swap.io/managed-by:
  llama-swap`. The wrapper polls pod readiness, streams the backend's logs
  into llama-swap's logs, and answers llama-swap's health check from pod
  readiness, so `checkEndpoint` can stay at the default `/health` for every
  engine.
- **Models**: a shared ReadWriteMany PVC (e.g. longhorn) holds the weights;
  backends mount it read-only at `/models`. RWX vs RWO vs emptyDir, and
  when each is right, is covered in the storage article below.

## Model config

A model that runs on Kubernetes needs `cmd`, `cmdStop` and `proxy` pointing
at the wrapper. Everything after `--` is the engine's own command line:

```yaml
models:
  krea2-turbo:
    proxy: "http://127.0.0.1:${PORT}"
    capabilities: { in: [text], out: [image] }
    cmd: >-
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model krea2-turbo
      --namespace {{ .Release.Namespace }}
      --image <backend image>
      --gpu amd.com/gpu=1
      --node-selector feature.node.kubernetes.io/amd-gpu=true
      --volume pvc:llama-swap-models:/models:ro
      --
      --diffusion-model /models/krea-2-turbo-Q4_K_M.gguf
      --listen-port 8080
    cmdStop: kubeswap delete --model krea2-turbo --namespace {{ .Release.Namespace }} --wait 60s
```

`--namespace` must be the namespace the head-end runs in: kubeswap's
RBAC (the chart's Role/RoleBinding) is namespace-scoped, so a different
namespace fails with 403s. The chart's `{{ .Release.Namespace }}`
placeholder keeps it correct; in a hand-rolled deployment or an existing
ConfigMap, set the release namespace literally.

One config gotcha: llama-swap substitutes only `${PID}` in `cmdStop` (not
`${PORT}`), so the model ID and namespace there are literal text.

Key flags:

- `--listen 127.0.0.1:${PORT}` — where the wrapper proxies; `${PORT}` is
  llama-swap's assigned port.
- `--command <binary>` — override the image entrypoint and run the chosen
  server binary. The unified image's entrypoint runs llama-swap itself
  (the head-end), so kubeswap models need this: `llama-server`,
  `sd-server`, `whisper-server`, `audiocpp_server`.
- `--health-path <path>` — the engine's real health endpoint, driving the
  in-pod readiness probe (`/health` default; `/v1/models` for sd-server).
- `--startup-timeout <dur>` — how long the startup probe tolerates model
  loading before restarting the pod (default 10m); it also gates
  readiness/liveness until the first answer. Keep llama-swap's global
  `healthCheckTimeout` at least this long.
- `--request name=quantity` / `--limit name=quantity` — generic resource
  overrides; `--gpu key=1` sets both request and limit for an exclusive
  device.
- `--service-port name:port` — extra container and Service ports
  (e.g. a metrics port).
- `--volume pvc:name:/mount[:ro]` and `--volume emptydir:/mount` — model
  cache, scratch, slot state.
- `--strict` — a Deployment whose spec drifted from the config is
  deleted and recreated instead of adopted. This interrupts the backend:
  the old pod is gone before the new one is ready, so in-flight requests
  are dropped and the model reloads from scratch. Enable it only when
  drift is expected (e.g. image bumps) and plan for the downtime.

## GPU and CPU resources

A GPU is **not** a requirement anywhere: the head-end needs none, and a
model without `--gpu` is a plain CPU pod that schedules on any node (the
whisper and TTS models in the examples article run exactly that way).
`--gpu key=count` sets the resource in both requests and limits — an
exclusive device assigned by the scheduler.

| hardware | `--gpu` | `--node-selector` |
| --- | --- | --- |
| NVIDIA | `nvidia.com/gpu=1` | the NVIDIA node label, or omit if every node has a GPU |
| AMD | `amd.com/gpu=1` | e.g. `feature.node.kubernetes.io/amd-gpu=true` |
| CPU only | omit | omit (or pin a node when RWO storage demands it) |

Other vendors' device plugins register their own resource names — read the
real one from `kubectl describe node` under `Capacity` rather than guessing.
On CPU, drop GPU-only engine flags too (`-ngl` for llama-server is a no-op,
`--diffusion-fa` for sd-server needs a GPU; CPU image generation works but
is slow).

## Engines

Any engine with a port works. Verified examples:

| engine | binary | health | API |
| --- | --- | --- | --- |
| llama.cpp | `llama-server --port 8080` | wrapper-answered `/health` | OpenAI chat/completions |
| stable-diffusion.cpp | `sd-server --listen-port 8080 --listen-ip 0.0.0.0` | wrapper-answered `/health` (probe: `--health-path /v1/models`) | `/v1/images/generations` |
| whisper.cpp | `whisper-server --host 0.0.0.0 --port 8080 --inference-path /v1/audio/transcriptions` (default bind is 127.0.0.1) | wrapper-answered `/health` | `/v1/audio/transcriptions` |
| audio.cpp | `audiocpp_server --config server.json` | native `/health` | `/v1/audio/speech` |

For audio.cpp the server reads a JSON config (model id, family, GGUF path,
voice presets); the config's model id must equal the llama-swap model id
because the server rejects unknown `model` request fields. Voice cloning
needs a reference WAV plus its transcript (`reference_text`) declared as a
voice preset, and the `voice` value in a request must name one of those
presets.

## Lifecycle semantics

- `kubeswap serve` **adopts**: if a live Deployment for the model already
  exists (left over from a crashed head end), it is reused — no model reload.
- **Two clean-shutdown behaviors, chosen per model.** With `cmdStop` (the
  usual story), a clean llama-swap shutdown or config reload stops every
  loaded model via `kubeswap delete` — backends are unloaded, nothing is
  left behind. Without `cmdStop`, the wrapper merely exits and keeps the
  Deployment, so backends *survive* a clean restart/reload and are adopted
  with no model reload. The same keep-and-adopt path covers abnormal deaths
  (SIGKILL, node loss) either way.
- A `kubeswap gc` init container on head-end start removes workloads whose
  model id is no longer in the config.
- Slot state (`--slot-save-path`) can live on an `emptyDir` (default) or a
  `pvc:` volume; see the phase-2 slot routing work.

## Deploying with Helm

The chart in `cmd/kubeswap/chart/` deploys the head-end end to
end from a checkout:

```bash
helm install llama-swap ./cmd/kubeswap/chart \
  -n llama-swap --create-namespace
```

It renders the ServiceAccount, the RBAC Role/RoleBinding, a ConfigMap from
`config.inline` (templated, so `{{ .Release.Namespace }}` works in model
commands; or `config.existing` for a pre-made one), the head-end Deployment
with a one-shot `kubeswap gc` init container, and a Service. Options (full
values table in the chart README):

- `ingress.enabled` / `gateway.enabled` — expose the endpoint (Ingress v1 or
  Gateway API; TLS is the standard ingress-level `tls` list).
- `service.type` — ClusterIP by default; `LoadBalancer` (with
  `loadBalancerIP`/`loadBalancerSourceRanges`) or `NodePort` work unchanged.
- `extraResources` — a list of full manifests rendered verbatim; the
  idiomatic home for a pre-staged model-cache PVC.

The default values need no PVC at all: one tiny model that llama-server
pulls from Hugging Face into a per-pod emptyDir. A complete multi-engine
`config.inline` is in the examples article below.

<!-- TODO: once the chart is published to a Helm repo / OCI registry, this
     becomes `helm repo add llama-swap <repo-url>` +
     `helm install llama-swap llama-swap/llama-swap`. -->

Until a release ships `kubeswap`, set `image.repository`/`image.tag` to an
image that does (the unified image built from a revision containing
`cmd/kubeswap/`).

## Hand-rolled RBAC

The chart renders a ServiceAccount + Role + RoleBinding; here is the same
pair verbatim, for deployments that do not use the chart. The rules are
namespaced and minimal — no `update`/`patch` anywhere on purpose: kubeswap
only creates and deletes (spec drift is handled by deleting and recreating
with `--strict`), so a tighter role is not a trade-off.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: llama-swap
  namespace: llama-swap
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: llama-swap
  namespace: llama-swap
rules:
  - apiGroups: ["apps"]
    resources: [deployments]
    verbs: [create, get, list, watch, delete]
  - apiGroups: [""]
    resources: [services]
    verbs: [create, get, list, watch, delete]
  - apiGroups: [""]
    resources: [persistentvolumeclaims]
    verbs: [get, create, delete]
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list, watch]
  - apiGroups: [""]
    resources: [pods/log]
    verbs: [get, list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: llama-swap
  namespace: llama-swap
subjects:
  - kind: ServiceAccount
    name: llama-swap
    namespace: llama-swap
roleRef:
  kind: Role
  name: llama-swap
  apiGroup: rbac.authorization.k8s.io
```

Bind the head-end Deployment to the ServiceAccount
(`serviceAccountName: llama-swap`) and keep `--namespace` in every model
command equal to it — the role is namespace-scoped, so anything else fails
with 403s.

## Related

- `examples/kubeswap-kubernetes` — complete multi-engine config (all four
  engine families, copy-pasteable)
- `guides/operations/debugging-backends` — the status → logs → kubectl
  failure chain and the symptom table
- `guides/operations/storage-options-kubernetes` — RWX vs RWO vs emptyDir
  for the model cache, and the node-pin escape
- `guides/operations/building-unified-image` — building the image, adding
  an engine, verifying kubeswap is inside
- `cmd/kubeswap/chart/` — the Helm chart (values reference,
  ingress/gateway/LoadBalancer/extraResources options)
- `guides/model-runtime/writing-cmd` — `cmd`, `${PORT}`, `proxy`, `checkEndpoint`
- `guides/configuration/macros` — cutting repetition out of long command lists
- `guides/model-runtime/ttl-and-unloading` — idle unloading and `cmdStop`
