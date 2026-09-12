# kubeswap

`kubeswap` is a wrapper program that lets llama-swap manage inference
backends in a Kubernetes namespace the same way it manages docker backends:
the model's `cmd` launches `kubeswap serve`, which creates (or adopts) a
Deployment + Service for the model and proxies a local port to the backend
pod; the model's `cmdStop` runs `kubeswap delete` to tear the objects down.

It is **engine-agnostic**: it runs any HTTP server container (llama-server,
sd-server, whisper-server, audiocpp_server, ...) and proxies the local port
to it. Engines differ only in the container args, the command to run, and
the health endpoint — all covered by flags. All four servers ship in the
unified llama-swap image, so a single `--image` covers every engine.

It provides five subcommands:

- `serve`: used as a model's `cmd`. Ensures the model's PVCs, Deployment and
  Service exist (adopting them if a previous run left them), then runs a
  forward proxy from `--listen` to the backend pod. The proxy answers
  `--check-path` (default `/health`) with 200/503 **from the pod's readiness
  state** and 503 for everything else until the pod is Ready, so
  llama-swap's health check gates on real readiness regardless of what
  health endpoint the engine itself exposes. Pod logs are forwarded to
  stderr. SIGTERM exits **without deleting anything** — the Deployment is
  kept so the next `serve` adopts it. (On a *clean* llama-swap shutdown or
  config reload, `cmdStop` runs first and unloads the backends; the
  keep-on-SIGTERM path is what matters for abnormal deaths — SIGKILL, node
  loss — and for models configured without a `cmdStop`.)
- `delete`: used as a model's `cmdStop`. Deletes the model's Deployment and
  Service (and the PVCs kubeswap itself created, with `--delete-volumes`),
  then waits for the pods to terminate so the GPU is released before
  llama-swap considers the stop done. A running `serve` process observes the
  deployment deletion and exits on its own.
- `gc`: one-shot garbage collection. Deletes managed workloads whose model
  is no longer in the given model list (`--models`) or llama-swap config
  (`--config path/to/config.yaml`). Use it to clean up leftovers from removed
  or renamed models.
- `status`: prints the managed workloads in the namespace.
- `version`: prints version and build information.

## Why use this?

llama-swap's lifecycle engine (TTL, preload, eviction, config reload) works
on anything that looks like a process with a local proxy port. `kubeswap`
gives a Kubernetes backend exactly that shape, so all of llama-swap's model
management applies unchanged: a model config with `cmd: kubeswap serve ...`
swaps in and out of the GPU just like a docker backend does.

Each model gets one pod (one instance per model), scheduled onto a GPU node
via `--gpu` + `--node-selector`, with the model weights on a (usually shared)
PVC and (for llama-server) slot state on an emptyDir or PVC for the engine's
built-in `--slot-save-path`.

## Scope and design

Deliberate boundaries for this first pass (the wrapper is meant to be
upstreamable on its own):

- **No CRDs, no llama-swap core changes.** `kubeswap` is its own `main` under
  `cmd/kubeswap/`; the only Kubernetes dependency (`client-go`) is confined
  to it. The llama-swap server binary stays free of Kubernetes code and
  never talks to the cluster — every model feature (group/matrix routers,
  TTL, unloading, preload, eviction, config reload) works unchanged.
- **One instance per model, no load balancing.** Each model gets exactly one
  pod; multi-instance scaling is explicitly out of scope for now.
- **Dumb proxy, no routing brain.** The `serve` proxy is a plain HTTP
  passthrough mirroring llama-swap's peer proxy for SSE
  (`X-Accel-Buffering: no`) and client cancellation — no API-key injection,
  model-name rewriting, or inflight accounting: all of llama-swap's
  middleware runs *before* the wrapper.
- **Poll, don't watch.** `serve` polls the cluster once per second
  (`--poll`) — deliberately not an informer: fake-clientset-testable, no
  warm-up/resync edge cases, and 1 s latency is irrelevant next to model
  load times.
- **No in-place spec updates.** Config changes flow through llama-swap's
  normal unload → reload lifecycle (delete → create); `--strict` covers
  manual drift on a live Deployment.
- **Deterministic rendering.** Flag → Deployment/Service/PVC rendering is a
  pure function, unit-tested without a cluster. Backend Deployments use
  `strategy: Recreate` (single instance — a rolling update would briefly
  run two pods fighting over the same GPU/PVC) and
  `terminationGracePeriodSeconds` from `--grace`.

## Prerequisites

- A Kubernetes cluster reachable with either:
  - an in-cluster ServiceAccount token (when llama-swap runs as a pod), or
  - a kubeconfig (`KUBECONFIG` or `~/.kube/config`) for a host-side
    llama-swap.
- RBAC for the namespace: the [Helm chart](chart/) renders a
  starter ServiceAccount + Role + RoleBinding (deployments/services
  create-get-list-watch-delete, PVCs get/create/delete, pods get/list/watch,
  pod logs, events); the rules are overridable via `rbac.rules`.
- A StorageClass that supports `ReadWriteMany` if the model cache PVC should
  be reachable from every GPU node (e.g. longhorn, nfs). kubeswap creates
  missing PVCs with `--pvc-size` / `--pvc-class` / `--pvc-access-mode`;
  pre-created PVCs are adopted as-is and never relabeled.

## Installation

Build the binary from source:

```bash
go build -o kubeswap ./cmd/kubeswap
# or: make kubeswap
```

Or use the unified llama-swap image, which ships `/usr/local/bin/kubeswap`
built from the same revision.

## Deploying the head-end with Helm

The chart in [`chart/`](chart/) deploys the whole
head-end: ServiceAccount + RBAC, the ConfigMap (from inline config), the
Deployment (with the `kubeswap gc` init container), the Service, and
optional Ingress / Gateway API exposure. Its full values reference and
worked examples are in the [chart README](chart/README.md).

From a checkout:

```bash
helm install llama-swap ./cmd/kubeswap/chart \
  -n llama-swap --create-namespace
```

<!-- TODO: once the chart is published to a Helm repo / OCI registry,
     replace the checkout install with:
       helm repo add llama-swap <repo-url>      # or: oci://<registry>/...
       helm install llama-swap llama-swap/llama-swap -n llama-swap --create-namespace -->

The default values are a zero-prerequisite demo: one tiny model (SmolLM2,
~135MB) that llama-server pulls from Hugging Face into a per-pod emptyDir —
`helm install` works with no PVC. Common scenarios:

- **Your own models** — `--set-file config.inline=/path/to/config.yaml`
  (templated, so `{{ .Release.Namespace }}` works in model commands); a
  complete multi-engine config is the
  [kubeswap examples article](../../docs/kb/examples/kubeswap-kubernetes.md).
- **Model cache PVC** — list it under `extraResources` (rendered verbatim,
  tracked by helm) and reference it from the models' `--volume` flags.
- **Existing ConfigMap** — `config.existing: <name>` (the chart renders no
  ConfigMap of its own).
- **Ingress / Gateway API** — `ingress.enabled` / `gateway.enabled` (create
  or attach to a Gateway); TLS is the standard ingress-level `tls` list.
- **LoadBalancer** — `service.type: LoadBalancer` (+ `loadBalancerIP` /
  `loadBalancerSourceRanges`); ClusterIP is the default.

Until a release ships `kubeswap`, point `image.repository`/`image.tag` at
an image that does (the unified image built from a revision with
`cmd/kubeswap/`; the release pipeline compiles it from the same revision
automatically once the branch is in a release).

## Usage in llama-swap

### As a model's `cmd`

Everything after `--` becomes the backend container's arguments.

The unified llama-swap image ships every backend (`llama-server`,
`sd-server`, `whisper-server`, `audiocpp_server`) alongside `kubeswap` in
`/usr/local/bin`. Its entrypoint runs llama-swap itself (the head-end), so
`--command` overrides it to run a backend server:

```yaml
models:
  lfm25-230m:
    proxy: 127.0.0.1:${PORT}
    cmd: |
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model lfm25-230m
      --namespace llama-swap
      --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
      --command llama-server
      --gpu amd.com/gpu=1
      --node-selector feature.node.kubernetes.io/amd-gpu=true
      --volume pvc:llama-swap-models:/models:ro
      --
      --model /models/LFM2.5-230M-Q4_0.gguf
      --port 8080
      --ctx-size 4096
      --threads 8
    cmdStop: kubeswap delete --model lfm25-230m --namespace llama-swap --wait 60s
    ttl: 30m
```

Any image with a proper server entrypoint works too (e.g. the published
`ghcr.io/ggml-org/llama.cpp:server-vulkan`) — `--command` is only needed
when the image's default command is not the server you want.

#### GPU (NVIDIA, `nvidia.com/gpu` device plugin)

```yaml
models:
  qwen25:
    proxy: 127.0.0.1:${PORT}
    cmd: |
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model qwen25
      --namespace llama-swap
      --image ghcr.io/mostlygeek/llama-swap:unified-cuda13
      --command llama-server
      --gpu nvidia.com/gpu=1
      --volume pvc:llama-swap-models:/models:ro
      --
      -hf bartowski/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M
      --port 8080
      --ctx-size 4096
    cmdStop: kubeswap delete --model qwen25 --namespace llama-swap
    ttl: 30m
```

#### CPU (no `--gpu`, no node selector; lavapipe Vulkan works)

```yaml
models:
  smollm2:
    proxy: 127.0.0.1:${PORT}
    cmd: |
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model smollm2
      --namespace llama-swap
      --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
      --command llama-server
      --volume pvc:llama-swap-models:/models:ro
      --
      --model /models/SmolLM2-135M-Instruct-Q4_K_M.gguf
      --port 8080
      --ctx-size 2048
      --threads 4
    cmdStop: kubeswap delete --model smollm2 --namespace llama-swap
    ttl: 30m
```

### Other engines (sd.cpp, whisper.cpp, audio.cpp, ...)

The wrapper is engine-agnostic, and llama-swap already routes image,
TTS/ASR and rerank endpoints, so other engines work end to end with config
alone. The unified llama-swap images ship the project's other backends
(`sd-server`, `whisper-server`, `audiocpp_server`) alongside `llama-server`
and `kubeswap` in `/usr/local/bin` — so one image covers every backend.
The image's entrypoint runs llama-swap (the head-end), so `--command`
overrides it: name the server binary you actually want to run.

Three things differ per engine:

1. `--command` — the server binary to run (only needed when the image's
   default command is not that server; the unified image needs it, its
   entrypoint runs llama-swap itself).
2. `--health-path` — the engine's **real** health endpoint; it drives the
   in-pod readiness probe, so it must return success once the engine is
   up (`/health` for llama-server/whisper-server/audiocpp_server,
   `/v1/models` for sd-server).
3. `--port` — the container's listen port (must match the engine's
   listen flag).

llama-swap's own health check (`checkEndpoint`, default `/health`) needs no
per-engine configuration: `serve` answers `--check-path` (default `/health`)
itself from pod readiness, independent of the engine's endpoints.

stable-diffusion.cpp (`sd-server`) serves its readiness endpoint at
`/v1/models` (its OpenAI route; there is no `/health`), and a model is
several files (diffusion model, VAE, text encoder, ...):

```yaml
models:
  krea-2-turbo:
    proxy: 127.0.0.1:${PORT}
    cmd: |
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model krea-2-turbo
      --namespace llama-swap
      --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
      --command sd-server
      --gpu amd.com/gpu=1
      --node-selector feature.node.kubernetes.io/amd-gpu=true
      --health-path /v1/models
      --volume pvc:llama-swap-models:/models:ro
      --
      --listen-port 8080
      --listen-ip 0.0.0.0
      --diffusion-model /models/Krea-2-Turbo-Q4_K_M.gguf
      --llm /models/Qwen3-VL-4B-Instruct-Q4_K_M.gguf
      --vae /models/wan_2.1_vae.safetensors
      --offload-to-cpu
    cmdStop: kubeswap delete --model krea-2-turbo --namespace llama-swap --wait 60s
    ttl: 7200
```

Note: `healthCheckTimeout` is a **global** llama-swap setting (top-level in
`config.yaml`), not per-model — a per-model entry is silently discarded.
Size the global value for the slowest backend; the image models above load
10-16GB of weights on their first request, so a small cluster typically
needs `healthCheckTimeout: 600` or more.

whisper.cpp (ASR) loads **Whisper-family** models only (`.bin`/`.gguf`
whisper and distil-whisper weights — not, e.g., parakeet or other CTC
models), has `/health`, so the probe defaults are fine. `--inference-path`
points the OpenAI-compatible route at the path llama-swap proxies:

```yaml
models:
  distil-whisper-lgv3:
    proxy: 127.0.0.1:${PORT}
    cmd: |
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model distil-whisper-lgv3
      --namespace llama-swap
      --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
      --command whisper-server
      --volume pvc:llama-swap-models:/models:ro
      # whisper.cpp's server binds 127.0.0.1 by default — pass --host 0.0.0.0
      --
      --host 0.0.0.0
      --port 8080
      --model /models/distil-large-v3-q5_0.bin
      --inference-path /v1/audio/transcriptions
    cmdStop: kubeswap delete --model distil-whisper-lgv3 --namespace llama-swap
    ttl: 7200
```

audio.cpp (TTS) reads a JSON server config; its `models[].id` **must equal
the llama-swap model id** (the server rejects unknown `model` request
fields). Voice cloning needs a reference WAV **and its transcript**
(`reference_text`); voice presets make OpenAI-style `{model, input, voice}`
bodies work — the `voice` value must be a preset name (a `voice` that does
not match a preset is an error, and the default preset is only used when
`voice` is absent, so declare the names clients send). The config file
lives on the model cache volume (or any mounted path):

```yaml
models:
  qwen3-tts-06b:
    proxy: 127.0.0.1:${PORT}
    cmd: |
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model qwen3-tts-06b
      --namespace llama-swap
      --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
      --command audiocpp_server
      --volume pvc:llama-swap-models:/models:ro
      --
      server
      --config /models/qwen3-tts-server.json
      --backend cpu
    cmdStop: kubeswap delete --model qwen3-tts-06b --namespace llama-swap
    ttl: 7200
```

```json
// /models/qwen3-tts-server.json
{
  "host": "0.0.0.0",
  "port": 8080,
  "models": [
    {
      "id": "qwen3-tts-06b",
      "family": "qwen3_tts",
      "path": "/models/qwen3-tts-0.6b-base-q8_0.gguf",
      "task": "tts",
      "mode": "offline",
      "voice_presets": {
        "alloy": {
          "voice_ref": "/models/tts-ref.wav",
          "reference_text": "Some call me nature. Others call me Mother Nature. I've been here for over 4.5 billion years. 22,500 times longer than you."
        },
        "default": {
          "voice_ref": "/models/tts-ref.wav",
          "reference_text": "Some call me nature. Others call me Mother Nature. I've been here for over 4.5 billion years. 22,500 times longer than you."
        }
      },
      "default_voice_preset_id": "default"
    }
  ]
}
```

All four engine families above (llama-server, sd-server, whisper-server,
audiocpp_server) have been verified end to end through llama-swap on a
k3s cluster: on-demand pod provisioning, streaming image/audio generation,
and `cmdStop` teardown. A complete copy-paste config with all four engines,
ready to feed the [Helm chart](chart/) as `config.inline`, is the
[`kubeswap` examples article](../../docs/kb/examples/kubeswap-kubernetes.md).

Notes:

- Model files all live on the model cache volume; the layout is yours —
  the container args just point at paths (directories mount like any other
  path).
- Engines with a health endpoint other than `/health`: point `--health-path`
  at it (or at any endpoint that only returns success once the engine is
  ready — `/v1/models` for sd-server). `--check-path` only matters if you
  want the wrapper's self-answered check at a different path than `/health`.
- llama-swap's slot/session management (phase 2, llama-swap core) applies
  only to llama-server models; other engines are unaffected. For
  llama-server, `--volume emptydir:slots:/slots` or
  `--volume pvc:<name>:/slots` gives the engine's built-in
  `--slot-save-path` somewhere to write.

### Naming

The Deployment name is the model ID translated to lowercase alphanumerics
and dashes (up to 50 chars) plus a short hash of the **original** ID
(e.g. `author/model:v1` → `author-model-v1-9a3b7c1d`); the Service is the
Deployment name plus `-svc`. The hash makes names collision-resistant:
distinct IDs that sanitize to the same string (`Model_A` and `model-a`)
or share a long prefix still get distinct objects. The original ID is
preserved in the `llama-swap.io/model-id` annotation; every object is
labeled `llama-swap.io/managed-by=llama-swap` and
`llama-swap.io/model=<sanitized>`. Adoption and deletion verify the
managed-by and model-id metadata against the requested model ID before
acting, so a model can never adopt or tear down another model's backend.
Pod *selection* (the Deployment and Service selectors, the wrapper's
proxy/health/log lookups, and delete waits) additionally uses the label
`llama-swap.io/deployment=<deployment name>`, which IS unique per model —
the sanitized `model` label alone is not, so sibling models with
sanitizingly-identical IDs can never be confused at the pod level either.
Adoption and deletion look objects up by exact name, never by that
coarse label, for the same reason.

Use the **configured model ID** in `cmdStop`/`gc`, not the sanitized name.

Note that llama-swap substitutes only `${PID}` in `cmdStop` (unlike `cmd`,
which also gets `${PORT}`) — the model ID and namespace in `cmdStop` must be
literal text.

### Volumes

`--volume` takes `kind:name:path[:ro]`, repeatable:

- `pvc:llama-swap-models:/models:ro` — existing or auto-created PVC
- `emptydir:slots:/slots` — per-pod scratch (KV slot state)
- `hostpath:/data/models:/models:ro` — path on the scheduled node

PVCs that already exist are adopted as-is (a shared model cache is typically
pre-created once, RWX, by the operator); missing ones are created with
`--pvc-size` (default `1Gi`), `--pvc-class` (default: cluster default) and
`--pvc-access-mode` (`rwo` or `rwx`). `delete --delete-volumes` only removes
PVCs kubeswap created itself (labeled); pre-created claims survive.

### Head-end in a cluster vs on the host

Both work. In-cluster, llama-swap runs as a pod with the `llama-swap`
ServiceAccount and `kubeswap` uses the pod's token; the proxy targets the
backend **pod IP** directly, so no extra network exposure is needed. On the
host, `kubeswap` falls back to the standard kubeconfig loading rules. If your
CNI does not let the head-end reach pod IPs, point `--upstream` at a
reachable URL (e.g. a manual port-forward) — the wrapper then uses it
verbatim instead of discovering the pod IP.

### Garbage collection

After removing models from your config, collect the orphaned workloads:

```bash
kubeswap gc --namespace llama-swap --config /etc/llama-swap/config/config.yaml
# or with an explicit allow-list:
kubeswap gc --namespace llama-swap --models lfm25-230m --models qwen25
```

A convenient pattern is an initContainer on the head-end that runs `gc` once
per start (the head-end config is already a ConfigMap mount):

```yaml
initContainers:
- name: kubeswap-gc
  image: <llama-swap image>
  command: ["/usr/local/bin/kubeswap", "gc", "--namespace", "llama-swap",
            "--config", "/etc/llama-swap/config/config.yaml"]
```

### Adoption and strict mode

If a Deployment with the model's name already exists, `serve` adopts it —
backends left over from an abnormal head-end death (SIGKILL, node loss) or a
model without a `cmdStop` keep running, with no model reload. With
`--strict`, a
deployment whose image/command/args/env/resources/probe paths drifted from
the config is deleted and recreated instead of adopted. The replacement
waits for the old deployment's deletion to actually complete (bounded at
5 minutes) before creating, and retries creation while the name is still
reserved — a successful Delete can return while the object is still
terminating, and creating against that window would lose the name
entirely (no replacement, and the next poll would shut the wrapper
down).

### Flag reference

`serve` (global `--namespace` default `llama-swap`, `--kubeconfig` default:
in-cluster config else standard kubeconfig rules):

| flag | default | meaning |
|---|---|---|
| `--listen` | (required) | local proxy address, e.g. `127.0.0.1:${PORT}` |
| `--model` | (required) | llama-swap model ID (source of object names) |
| `--image` | (required) | backend container image |
| `--port` | `8080` | container listen port (probe + upstream target) |
| `--command` | (image entrypoint) | container command token, repeatable; exec'd directly (no shell), overriding the image's entrypoint — the unified image needs it (its entrypoint runs llama-swap, not a backend server); use the full path if the binary is not on the container's `PATH` |
| `--health-path` | `/health` | backend health endpoint (readiness probe) |
| `--check-path` | `/health` | path the wrapper answers itself from pod readiness (200 ready / 503 + reason); point consumers' health checks here |
| `--liveness-path` | (health-path) | backend liveness probe endpoint |
| `--probe-timeout` | `5s` | readiness/liveness probe request timeout |
| `--startup-timeout` | `10m` | model loading time the **startup probe** tolerates before the pod restarts; it also gates readiness/liveness until the backend answers once (llama-swap's global `healthCheckTimeout` should be at least this long) |
| `--gpu` | — | GPU resource, e.g. `amd.com/gpu=1` (sets requests **and** limits — GPUs are exclusive) |
| `--request` | — | resource request `name=quantity`, e.g. `cpu=2`, `memory=4Gi` (repeatable; overrides `--gpu` for the same key) |
| `--limit` | — | resource limit `name=quantity`, e.g. `cpu=4` (repeatable; overrides `--gpu` for the same key) |
| `--service-port` | — | extra Service port `name:port` (repeatable; e.g. expose backend metrics) |
| `--env` | — | `KEY=VALUE` container env, repeatable |
| `--node-selector` | — | `key=value`, repeatable |
| `--toleration` | — | `key:operator:value:effect` (operator Equal or Exists, default Equal; effect NoSchedule/PreferNoSchedule/NoExecute or empty for any effect; value must be empty with Exists), repeatable |
| `--label` | — | extra pod label `K=V`, repeatable |
| `--volume` | — | `pvc:name:path[:ro]`, `emptydir:name:path[:ro]` or `hostpath:nodePath:mountPath[:ro]`, repeatable |
| `--pvc-size` | `1Gi` | size for PVCs kubeswap must create |
| `--pvc-class` | cluster default | StorageClass for created PVCs |
| `--pvc-access-mode` | `rwo` | `rwo` or `rwx` for created PVCs |
| `--grace` | `30s` | pod terminationGracePeriodSeconds |
| `--strict` | `false` | replace a drifted deployment instead of adopting it |
| `--upstream` | discovered pod IP | fixed proxy upstream URL |
| `--poll` | `1s` | cluster state poll interval |
| `--no-logs` | `false` | disable pod log forwarding |
| `--` | — | everything after becomes the container args |

`delete`: `--model` (required), `--delete-volumes`, `--wait` (default `30s`;
`0` = do not wait). Keep `--wait` (and `--grace`) within llama-swap's
`unloadTimeout` — llama-swap gives up on the stop when that budget expires
and kills the wrapper, which can leave the pod terminating behind it.

`gc`: `--models` (repeatable or comma-separated allow-list), `--config`
(llama-swap config.yaml — its model keys survive), `--delete-volumes`.

`status`: none beyond the global flags.

`version`: none.

### Troubleshooting

- `kubeswap status` — quick view of models, pods and readiness.
- `serve` forwards pod logs to stderr with a `[pod/<name>]` prefix, which
  llama-swap records in its log monitor.
- While the pod is not Ready, the proxy answers every request with 503 and
  JSON `{"status":"not-ready","reason":"..."}` (pod phase, waiting reason,
  restart counts) — the reason is visible in llama-swap's health checks.
- `kubeswap status`/`gc`/`delete` all take `--namespace` (default
  `llama-swap`); pass it explicitly when the release lives in another
  namespace (the RBAC role is namespaced).
- If log forwarding is refused by RBAC it is disabled once with a warning
  (transient "container still starting" errors are retried automatically).

`kubeswap` is part of the llama-swap project and documented here in that
context; it does work standalone against any HTTP server container
(kubeconfig + the `--listen` port), should anyone want that.
