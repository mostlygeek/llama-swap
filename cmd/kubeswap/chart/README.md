# llama-swap Helm chart

Installs the llama-swap head-end (router + lifecycle manager) into a
namespace, with the ServiceAccount and RBAC it needs, a ConfigMap holding
`config.yaml`, a Service, and optional Ingress / Gateway API exposure.
Because the head-end runs `kubeswap` as its `cmd`/`cmdStop` for every
model, the whole inference fleet — backend pods, their PVCs, teardown —
stays inside the same namespace the chart manages.

The head-end image must ship `/usr/local/bin/llama-swap` and
`/usr/local/bin/kubeswap`. The unified llama-swap images do, and also
ship every backend server (`llama-server`, `sd-server`,
`whisper-server`, `audiocpp_server`), so one image covers the head-end
and all backend pods.

## Install

From a checkout of llama-swap (the chart lives in
`cmd/kubeswap/chart/`):

```bash
helm install llama-swap ./cmd/kubeswap/chart \
  -n llama-swap --create-namespace
```

The chart never renders a Namespace object — create the release
namespace yourself (`--create-namespace` above) or point the install
at one that already exists.

<!-- TODO: the chart's home (Helm repo / OCI registry) is not decided yet.
     Once it is, add the canonical install here:
       helm repo add llama-swap <repo-url>        # or: oci://<registry>/...
       helm install llama-swap llama-swap/llama-swap -n llama-swap --create-namespace
     and version the chart against llama-swap releases (see Chart.yaml). -->

The default `values.yaml` is a demo config: one tiny model (SmolLM2,
~135MB) that llama-server downloads from Hugging Face on demand into a
per-pod emptyDir — no PVC required. For real use, override
`config.inline` with your models and point them at a model cache (see
"Model cache PVC" below).

```bash
helm install llama-swap cmd/kubeswap/chart -n llama-swap --create-namespace \
  --set-file config.inline=/path/to/your-config.yaml
```

`config.inline` is processed as a Go template, so `{{ .Release.Namespace }}`
works inside model commands (it is how the default config keeps
`--namespace` correct). llama-swap's own `${PORT}` macro is unaffected.

## Values

| key | default | meaning |
| --- | --- | --- |
| `image.repository` | `ghcr.io/mostlygeek/llama-swap` | head-end image |
| `image.tag` | `unified-vulkan` | `unified-cuda13` / `unified-cuda` for NVIDIA |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | list of secret names |
| `replicas` | `1` | must be 1 (the chart fails on more) — never two head-ends per namespace |
| `strategy.type` | `Recreate` | deliberate (see replicas) |
| `config.inline` | demo config | `config.yaml`, templated, into the chart's ConfigMap |
| `config.top` | `{}` | structured: everything except `models:` (scalars, macros, routing); templated |
| `config.defaults` | `{}` | structured: per-model defaults (see "Structured config") |
| `config.models` | `[]` | structured: list of model entries; non-empty ⇒ `config.inline` is ignored |
| `config.existing` | `""` | use this ConfigMap instead (chart renders none) |
| `config.extraFiles` | `{}` | extra ConfigMap keys (e.g. an audio.cpp server JSON) |
| `serviceAccount.create` | `true` | |
| `serviceAccount.name` | fullname | |
| `serviceAccount.annotations` | `{}` | e.g. workload-identity |
| `rbac.create` | `true` | namespaced Role + RoleBinding for kubeswap |
| `rbac.rules` | (kubeswap minimum) | deployments/services CRUD, PVC get/create/delete, pods get/list/watch, pods/log |
| `service.type` | `ClusterIP` | `LoadBalancer` / `NodePort` work unchanged |
| `service.port` | `8080` | |
| `service.annotations` | `{}` | |
| `service.loadBalancerIP` | `""` | LoadBalancer only |
| `service.loadBalancerSourceRanges` | `[]` | LoadBalancer only |
| `service.externalTrafficPolicy` | unset | LoadBalancer/NodePort only |
| `ingress.enabled` | `false` | networking.k8s.io/v1 Ingress |
| `ingress.className` | `""` | |
| `ingress.annotations` | `{}` | |
| `ingress.hosts` | `llama-swap.local` | list of `{host, paths: [{path, pathType}]}` |
| `ingress.tls` | `[]` | standard ingress TLS blocks |
| `gateway.enabled` | `false` | Gateway API; with `name` and `parentRefs` both empty, rendering fails |
| `gateway.gateway.name` | `""` | set to have the chart create a Gateway |
| `gateway.gateway.className` | `""` | required when `name` is set (e.g. `traefik`) |
| `gateway.gateway.listeners` | one HTTP :80 | |
| `gateway.route.name` | fullname | HTTPRoute name |
| `gateway.route.parentRefs` | the chart's Gateway | required when `name` is empty (attach to an existing Gateway) |
| `gateway.route.hostnames` | `[]` | |
| `gcInitContainer.enabled` | `true` | one-shot `kubeswap gc` per head-end start |
| `gcInitContainer.securityContext` | no-escalation, read-only root FS, drop ALL | for the gc init container; `runAsNonRoot` is not defaulted (stock image runs as root) |
| `podAnnotations` / `podLabels` | `{}` | |
| `priorityClassName` | `""` | |
| `nodeSelector` / `tolerations` / `affinity` | standard | schedule the head-end (it needs no GPU) |
| `podSecurityContext` / `securityContext` | `{}` | |
| `resources` | `{}` | |
| `env` / `envFrom` | `[]` | |
| `extraVolumes` / `extraVolumeMounts` | `[]` | head-end pod escape hatches |
| `extraContainers` / `initContainers` | `[]` | sidecars / extra init containers |
| `readinessProbe` / `livenessProbe` | `/health` | |
| `extraResources` | `[]` | full manifests, rendered verbatim |

## Examples

### Model cache PVC via extraResources

The idiomatic multi-node setup: one ReadWriteMany PVC, pre-staged with
model weights, referenced from the models' `kubeswap --volume` flags:

```yaml
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

Objects without an explicit namespace get the release namespace; helm
tracks them, so `helm uninstall` removes them too.

### Multi-engine config

Any of the four engine families works in `config.inline`; a complete
verified config (llama-server, sd-server, whisper-server,
audiocpp_server, six models across exclusive swap groups) is
[the kubeswap examples article](../../../../docs/kb/examples/kubeswap-kubernetes.md).
Replace the demo `smollm2` model with entries like:

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
      --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
      --command sd-server
      --health-path /v1/models
      --gpu amd.com/gpu=1
      --node-selector feature.node.kubernetes.io/amd-gpu=true
      --volume pvc:llama-swap-models:/models:ro
      --
      --diffusion-model /models/krea-2-turbo-Q4_K_M.gguf
      --llm /models/Qwen3VL-4B-Instruct-Q4_K_M.gguf
      --vae /models/wan_2.1_vae.safetensors
      --listen-ip 0.0.0.0
      --listen-port 8080
      --offload-to-cpu
    cmdStop: kubeswap delete --model krea2-turbo --namespace {{ .Release.Namespace }} --wait 60s
    ttl: 7200
```

### Structured config (many models)

For fleets, `config.models` replaces writing the `kubeswap serve`
boilerplate per model: the chart generates each model's `proxy`,
`cmd` (the whole `kubeswap serve ... -- <args>` line) and `cmdStop`.
`config.top` carries everything else (scalars, llama-swap `macros`,
routing) and `config.defaults` holds what most models share. A key on a
model entry overrides the matching default — an empty list clears it
(`gpu: []` for a CPU-only model).

Kubeswap fields consumed by the chart: `id` (required), `image`,
`command`, `gpu`, `volumes`, `port`, `extraKubeArgs` (verbatim
`kubeswap serve` flags before `--`, e.g. `"--node-selector k=v"`,
`"--request cpu=4"`), `startupTimeout`, `healthPath`, `livenessPath`,
`checkPath`, `args` (backend command line after `--`), `proxy`,
`cmdStop`. Every other key renders verbatim as a llama-swap model field
(`name`, `ttl`, `capabilities`, `macros`, `filters`, `aliases`, ...).

```yaml
config:
  top:                        # templated like config.inline
    healthCheckTimeout: 600
    macros:                   # llama-swap macros keep the args DRY
      server_base: --port 8080 -ngl 99
      sd_base: --listen-ip 0.0.0.0 --listen-port 8080 --diffusion-fa --offload-to-cpu
  defaults:
    image: ghcr.io/mostlygeek/llama-swap:unified-vulkan
    command: llama-server
    gpu: [amd.com/gpu=1]
    volumes: [pvc:llama-swap-models:/models:ro]
    extraKubeArgs: ["--node-selector feature.node.kubernetes.io/amd-gpu=true"]
    ttl: 1800
  models:
    - id: lfm25-230m
      args: ${server_base} --model /models/LFM2.5-230M-Q4_0.gguf
    - id: krea2-turbo
      command: sd-server
      healthPath: /v1/models
      capabilities: { in: [text], out: [image] }
      args: ${sd_base} --diffusion-model /models/krea.gguf --llm /models/llm.gguf --vae /models/vae.safetensors
    - id: whisper
      command: whisper-server
      gpu: []                 # empty list clears the default
      args: --host 0.0.0.0 --port 8080 --model /models/whisper.bin
```

`args` still composes with llama-swap's `${...}` macros from
`top.macros`. One limit: model maps render with sorted keys and
llama-swap expands macros in reverse declaration order, so a model
macro must not reference another model macro of the same model (global
macros and literals are fine).

### Ingress

```yaml
ingress:
  enabled: true
  className: traefik
  hosts:
    - host: llama-swap.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - hosts: [llama-swap.example.com]
      secretName: llama-swap-tls
```

### LoadBalancer service

```yaml
service:
  type: LoadBalancer
  loadBalancerSourceRanges: ["203.0.113.0/24"]
```

### Gateway API

```yaml
gateway:
  enabled: true
  gateway:
    name: shared-gw            # omit to route against an existing Gateway
    className: traefik
    listeners:
      - name: http
        protocol: HTTP
        port: 80
        hostname: llama-swap.example.com # singular: a Gateway listener field
  route:
    hostnames: ["llama-swap.example.com"] # plural: the HTTPRoute field
```

### Existing ConfigMap

```yaml
config:
  existing: my-llama-swap-config   # must contain a config.yaml key
```

## Notes

- **Compatibility**: the chart and the kubeswap binary in the image are
  versioned together in this repo; this chart is tested against kubeswap
  as it exists on this branch. If you mix a newer image with an older
  chart (or vice versa), check `rbac.rules`, the probe values and the
  config rendering against the kubeswap changes in between.
- **Upgrade behavior**: `helm upgrade` changes to `config.inline` update
  the ConfigMap; the head-end runs with `-watch-config`, so model
  changes apply on reload (added/removed models are unloaded/reloaded
  through the normal `cmd`/`cmdStop` lifecycle). Changing pod labels
  (`selectorLabels`) is not supported — helm's immutable selector.
- **GPU**: the head-end needs no GPU. Backends request GPUs from inside
  `config.inline` (`kubeswap --gpu ... --node-selector ...`), so the
  chart stays GPU-agnostic.
- **Uninstall**: `helm uninstall` removes head-end, RBAC and
  `extraResources`, but **not** backend workloads created at runtime —
  stop the head-end first (or run
  `kubeswap gc --namespace <ns> --config <path>` /
  `kubectl -n <ns> delete deploy -l llama-swap.io/managed-by=llama-swap`).
