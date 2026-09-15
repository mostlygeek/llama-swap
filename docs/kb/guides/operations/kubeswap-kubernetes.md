---
title: Running inference servers in Kubernetes with kubeswap
summary: Use the kubeswap wrapper as cmd/cmdStop so llama-swap starts and stops backend servers as Kubernetes workloads in a dedicated namespace.
category: guides
tags: [kubernetes, kubeswap, deployment, k8s, operator, pod, namespace, pvc, gpu, multi-backend, sd-server, whisper-server, audiocpp_server]
config_keys: [models.*.cmd, models.*.cmdStop, models.*.proxy, models.*.checkEndpoint, models.*.capabilities, healthCheckTimeout, unloadTimeout, routing]
updated: 2026-09-14
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
- **A crashed backend fails fast.** When the pod's container crashes
  (Terminated or CrashLoopBackOff), `serve` flushes the pod log tail,
  deletes the model's Deployment and Service, and exits with the backend's
  exit code. llama-swap then reports the load failure within seconds
  ("upstream command exited prematurely") instead of waiting out
  `healthCheckTimeout`, and a model that was already running transitions
  to stopped. The crash output is in the forwarded pod logs; fix the root
  cause and the next request rebuilds the backend.
- A `kubeswap gc` init container on head-end start removes workloads whose
  model id is no longer in the config.
- Slot state (`--slot-save-path`) can live on an `emptyDir` (default) or a
  `pvc:` volume; see the phase-2 slot routing work.

## Deploying with Helm

The chart deploys the head-end end to end. It is published to an OCI
registry (one version per llama-swap release — tag `vNNN` publishes
chart `NNN.0.0` with appVersion `NNN`; the published chart ships
`image.tag` unset and derives its default image from the app version,
`unified-vulkan-<appVersion>`, whose versioned docker tag the publish
workflow mints as an alias of the floating manifest):

```bash
helm repo add llama-swap oci://ghcr.io/mostlygeek/llama-swap-helm
helm install llama-swap llama-swap/llama-swap \
  -n llama-swap --create-namespace \
  --version 256.0.0    # the chart version for release v256; omit for latest
```

For development, install from a checkout instead (the chart lives in
`cmd/kubeswap/chart/`, floating image tag by default):

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

The head-end pod also carries a `checksum/config` annotation over the
rendered ConfigMap: the pod already hot-reloads via `-watch-config`, but a
failed reload only logs a warning and keeps serving the old config, so the
annotation makes config drift visible as a rollout.

### Matrix router builder

For a model fleet, `config.matrix` generates the llama-swap `routing`
section from the `config.models` roster, so the matrix DSL is never written
by hand. It replaces a `routing:` block under `config.top` (defining both
fails rendering) and requires `config.models` — it is not available with
`config.inline` or `config.existing`. The groups-and-matrix article covers
how the generated router behaves; this section covers what the builder
generates.

Pool membership comes from each model's `gpu` list, so the same flags that
size the pod decide the routing pool: a model with a non-empty merged `gpu`
list (the usual `defaults.gpu`) is a **GPU model**; `gpu: []` marks a model
**CPU-only**, which runs on any node and never counts against the GPU
budget.

```yaml
config:
  matrix:
    builder: gpu-budget        # gpu-budget | pools | manual
    budget: 1                  # how many GPU models may run at once
    evict_costs:               # optional: model id -> cost (default 1); a
      krea2-turbo: 10           # high cost makes the router evict other
      ideogram4: 10             # models first
    exclusive: []              # optional: models that run alone, outside the budget
```

**`gpu-budget`** renders "any *N* of the GPU models may run; CPU models are
unlimited":

```yaml
routing:
  router:
    use: matrix
    settings:
      matrix:
        sets:
          gpu_pool: (lfm25-230m | krea2-turbo | ideogram4)
          cpu_pool: (distil-whisper-lgv3 & qwen3-tts-06b)
          all: +gpu_pool & +cpu_pool
```

Each `+gpu_pool` is one slot, so `budget: 1` allows exactly one GPU model at
a time plus every CPU model. The budget is a hard cap — a set never contains
more than *N* GPU models, CPU models are never evicted to make room for GPU
ones — and every model lands in a pool automatically (a model in no set can
only run alone).

**`pools`** generalizes that to any number of named pools, each with its own
per-pool `budget` (omit it for a pool whose members all coexist); every model
entry then needs a `pool:` key. It covers category spreads like "1 big LLM,
1 TTS, up to 2 image models" — one pool per category. Fine-grained
displacement rules *between* pools are beyond the builder.

**`manual`** is the escape hatch: it renders `vars`/`evict_costs`/`sets`
verbatim, exactly as you would write them under `config.top.routing`.

`exclusive:` models are left out of the generated pools and get their own
single-member set, so they evict everything and are evicted by everything.
Model ids used by the generated builders must match the matrix identifier
charset (`[A-Za-z0-9._-]`); anything else fails rendering with a message
instead of breaking the router at runtime. The full values table, including
the `pools` and `manual` shapes, is in the chart README.

The published chart ships `image.tag` unset and derives its default
image from the app version (`unified-vulkan-<appVersion>`) rather than
codifying the version into the tag string. The publish workflow mints
the versioned docker tags `unified-<variant>-NNN` as manifest aliases of
the current floating `unified-<variant>` tags (digest logged) so the
names the chart references exist; a checkout install defaults to the
floating tag. To use a different variant or a hand-built image, override
`image.repository`/`image.tag`.

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
