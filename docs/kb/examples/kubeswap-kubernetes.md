---
title: "kubeswap: a complete multi-engine Kubernetes configuration"
summary: A verified llama-swap config that runs llama-server, sd-server, whisper-server and audiocpp_server as Kubernetes workloads through the kubeswap wrapper.
category: examples
tags: [kubeswap, kubernetes, multi-backend, sd-server, whisper-server, audiocpp_server, llama-server, pvc, gpu, exclusive-group]
config_keys: [models.*.cmd, models.*.cmdStop, models.*.proxy, models.*.capabilities, healthCheckTimeout, unloadTimeout, routing]
updated: 2026-09-12
---

# kubeswap: a complete multi-engine Kubernetes configuration

A copy-pasteable `config.yaml` that serves four engine families behind one
llama-swap endpoint: chat (llama-server), image generation (sd-server),
speech recognition (whisper-server) and text-to-speech (audiocpp_server).
Every model is a Kubernetes workload managed through the `kubeswap`
wrapper — no special backend keyword, just `cmd`/`cmdStop`. All four
servers ship in **one image** (the unified llama-swap image), so every
model below uses the same `--image` and only `--command` selects the
server. This exact shape was verified end to end on a k3s cluster
(request in, generated artifact out, `cmdStop` teardown).

Prerequisites: a head-end deployment — the Helm chart in
`cmd/kubeswap/chart/` is the standard path (below). The head-end
image must ship `/usr/local/bin/kubeswap` (a release, or an image built from
a revision containing `cmd/kubeswap/`). For a model-cache PVC instead of
emptyDirs, list it under the chart's `extraResources` (rendered verbatim and
tracked by helm) and point the models' `--volume` flags at it.

## Model cache layout

All weights live on the shared ReadWriteMany PVC (`llama-swap-models`),
mounted read-only at `/models` in every backend pod. The layout is your
choice — the container args just point at paths:

```
/models/
  LFM2.5-230M-Q4_0.gguf
  krea-2-turbo-Q4_K_M.gguf
  Qwen3VL-4B-Instruct-Q4_K_M.gguf
  wan_2.1_vae.safetensors
  ideogram4-Q4_0.gguf
  ideogram4_uncond-Q4_0.gguf
  Qwen3-VL-8B-Instruct-Q4_K_M.gguf
  flux2-vae.safetensors
  distil-large-v3-q5_0.bin
  qwen3-tts-0.6b-base-q8_0.gguf
  tts-ref.wav
  qwen3-tts-server.json
```

## config.yaml

```yaml
# kubernetes models are just `cmd`/`cmdStop` pointing at the kubeswap
# wrapper. healthCheckTimeout is global (not per-model) and must cover the
# slowest backend: the image models load ~10-16GB of weights on first
# request, so size it accordingly (and keep it >= --startup-timeout).
healthCheckTimeout: 600
unloadTimeout: 60        # seconds to wait for cmdStop (pod termination)

routing:
  router:
    use: group
    settings:
      groups:
        # Exclusive swap groups: one member at a time (single GPU).
        # Add more members to a group and they swap on request.
        llm:
          swap: true
          exclusive: true
          members: [lfm25-230m]
        image:
          swap: true
          exclusive: true
          members: [krea2-turbo, ideogram4]
        audio:
          swap: true
          exclusive: true
          members: [distil-whisper-lgv3, qwen3-tts-06b]

models:
  # --- chat (llama.cpp) --------------------------------------------------
  # The unified image's entrypoint runs llama-swap (the head-end); --command
  # overrides it to run a backend server instead. llama-server lives at
  # /usr/local/bin/llama-server in the image.
  lfm25-230m:
    proxy: "http://127.0.0.1:${PORT}"
    cmd: >-
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model lfm25-230m
      --namespace llama-swap
      --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
      --command llama-server
      --gpu amd.com/gpu=1
      --node-selector feature.node.kubernetes.io/amd-gpu=true
      --volume pvc:llama-swap-models:/models:ro
      --volume emptydir:slots:/slots
      --
      --model /models/LFM2.5-230M-Q4_0.gguf
      --port 8080
      -ngl 99
      -np 2
      --cache-ram 8
      --slot-save-path /slots
    cmdStop: kubeswap delete --model lfm25-230m --namespace llama-swap --wait 60s
    ttl: 1800

  # --- image generation (stable-diffusion.cpp) ---------------------------
  # One sd-server is several files: diffusion model, VAE, text encoder,
  # (optionally an unconditional model). sd-server binds 127.0.0.1 by
  # default (--listen-ip 0.0.0.0) and has no /health route
  # (--health-path /v1/models).
  krea2-turbo:
    proxy: "http://127.0.0.1:${PORT}"
    capabilities: { in: [text], out: [image] }
    cmd: >-
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model krea2-turbo
      --namespace llama-swap
      --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
      --command sd-server
      --health-path /v1/models
      --gpu amd.com/gpu=1
      --node-selector feature.node.kubernetes.io/amd-gpu=true
      --request cpu=4
      --volume pvc:llama-swap-models:/models:ro
      --
      --diffusion-model /models/krea-2-turbo-Q4_K_M.gguf
      --llm /models/Qwen3VL-4B-Instruct-Q4_K_M.gguf
      --vae /models/wan_2.1_vae.safetensors
      --listen-ip 0.0.0.0
      --listen-port 8080
      --diffusion-fa
      --offload-to-cpu
      --cfg-scale 1.0
      -H 1024 -W 1024
    cmdStop: kubeswap delete --model krea2-turbo --namespace llama-swap --wait 60s
    ttl: 1800

  ideogram4:
    proxy: "http://127.0.0.1:${PORT}"
    capabilities: { in: [text], out: [image] }
    cmd: >-
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model ideogram4
      --namespace llama-swap
      --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
      --command sd-server
      --health-path /v1/models
      --gpu amd.com/gpu=1
      --node-selector feature.node.kubernetes.io/amd-gpu=true
      --request cpu=4
      --volume pvc:llama-swap-models:/models:ro
      --
      --diffusion-model /models/ideogram4-Q4_0.gguf
      --uncond-diffusion-model /models/ideogram4_uncond-Q4_0.gguf
      --llm /models/Qwen3-VL-8B-Instruct-Q4_K_M.gguf
      --vae /models/flux2-vae.safetensors
      --listen-ip 0.0.0.0
      --listen-port 8080
      --diffusion-fa
      --offload-to-cpu
      --cfg-scale 1.0
      -H 1024 -W 1024
    cmdStop: kubeswap delete --model ideogram4 --namespace llama-swap --wait 60s
    ttl: 1800

  # --- speech recognition (whisper.cpp) ----------------------------------
  # CPU is fine for distil-large-v3. whisper-server binds 127.0.0.1 by
  # default (--host 0.0.0.0) and only loads Whisper-family weights
  # (.bin/.gguf whisper or distil-whisper).
  distil-whisper-lgv3:
    proxy: "http://127.0.0.1:${PORT}"
    capabilities: { in: [audio], out: [text] }
    cmd: >-
      kubeswap serve
      --listen 127.0.0.1:${PORT}
      --model distil-whisper-lgv3
      --namespace llama-swap
      --image ghcr.io/mostlygeek/llama-swap:unified-vulkan
      --command whisper-server
      --volume pvc:llama-swap-models:/models:ro
      --
      --host 0.0.0.0
      --port 8080
      --model /models/distil-large-v3-q5_0.bin
      --inference-path /v1/audio/transcriptions
    cmdStop: kubeswap delete --model distil-whisper-lgv3 --namespace llama-swap --wait 60s
    ttl: 1800

  # --- text to speech (audio.cpp) ----------------------------------------
  # audiocpp_server reads a JSON config (below); its models[].id MUST equal
  # the llama-swap model id — the server rejects unknown `model` request
  # fields. CPU backend keeps it off the GPU.
  qwen3-tts-06b:
    proxy: "http://127.0.0.1:${PORT}"
    capabilities: { in: [text], out: [audio] }
    cmd: >-
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
    cmdStop: kubeswap delete --model qwen3-tts-06b --namespace llama-swap --wait 60s
    ttl: 1800
```

NVIDIA clusters use `--gpu nvidia.com/gpu=1` and drop the AMD node
selector (or point it at your own GPU label). CPU-only: drop
`--gpu`/`--node-selector` (and `--diffusion-fa` for image models).

### /models/qwen3-tts-server.json

```json
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

The `voice` value in a request must name one of these presets (the default
preset is only used when `voice` is absent). Voice cloning needs the
reference WAV **and its exact transcript** (`reference_text`). Use the
self-contained GGUF package from `audio-cpp/audio.cpp-gguf` for qwen3_tts —
older-format files fail to load.

## Deploying this config

Save the config above as `config.yaml` (keep the `{{ .Release.Namespace }}`
tokens — the chart templates the inline config) and install the chart from a
checkout with it inlined:

```bash
helm install llama-swap ./cmd/kubeswap/chart \
  -n llama-swap --create-namespace \
  --set-file config.inline=./config.yaml
```

Optional exposure and storage (see the chart README for the full values
list):

```yaml
ingress:            # or gateway.enabled for Gateway API
  enabled: true
  className: traefik
  hosts:
    - host: llama-swap.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:              # ingress-level list (k8s has no per-host tls)
    - secretName: llama-swap-tls
      hosts: [llama-swap.example.com]
service:
  type: LoadBalancer      # ClusterIP is the default
  loadBalancerSourceRanges: ["203.0.113.0/24"]
extraResources:      # full manifests, rendered verbatim
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

The chart's own defaults need no PVC (one tiny model, pulled from
Hugging Face into a per-pod emptyDir), so `helm install` with no values
file works out of the box. `helm upgrade` with a changed
`config.inline` hot-reloads the models (the head-end runs with
`-watch-config`); changing `selectorLabels` is not supported.

## Talking to it

All four engines sit behind the head-end's `${START_PORT}` service:

```bash
LS=http://<head-end>:8080

# chat (llama-server)
curl -s $LS/v1/chat/completions -H 'Content-Type: application/json' \
  -d '{"model":"lfm25-230m","messages":[{"role":"user","content":"Say hello"}],"max_tokens":50}'

# image (sd-server) — OpenAI-style JSON, PNG arrives base64 in data[0].b64_json
curl -s $LS/v1/images/generations -H 'Content-Type: application/json' \
  -d '{"model":"krea2-turbo","prompt":"a red cube on a blue floor","size":"1024x1024"}' \
  | jq -r '.data[0].b64_json' | base64 -d > out.png

# speech recognition (whisper-server) — multipart
curl -s $LS/v1/audio/transcriptions \
  -F model=distil-whisper-lgv3 -F file=@recording.wav

# text to speech (audiocpp_server) — WAV bytes back
curl -s $LS/v1/audio/speech -H 'Content-Type: application/json' \
  -d '{"model":"qwen3-tts-06b","input":"Hello from Kubernetes","voice":"alloy"}' -o out.wav
```

## Gotchas

- `healthCheckTimeout` is **global** — a per-model entry is silently
  discarded. Size it for the slowest backend and keep it at or above
  `kubeswap`'s `--startup-timeout` (default 10m), which bounds how long
  the pod may take to load its model before being restarted.
- sd-server and whisper-server both bind **127.0.0.1 by default** — pass
  `--listen-ip 0.0.0.0` / `--host 0.0.0.0` or nothing outside the pod can
  reach them (symptom: pod Running, never Ready).
- sd-server has no `/health` route — readiness uses `--health-path
  /v1/models`. llama-swap's own health check stays at the wrapper's
  self-answered `/health` regardless.
- audio.cpp: config model id == llama-swap model id; `voice` must name a
  declared preset.
- The unified image's entrypoint runs **llama-swap** (the head-end), not a
  backend server — `--command` overrides it to run the chosen server
  binary (`llama-server`, `sd-server`, `whisper-server`,
  `audiocpp_server`). Every server ships in `/usr/local/bin` of one image,
  so the same `--image` works for every engine.
- First request to an image model loads 10-16GB of weights — expect
  minutes, not seconds, before the first PNG.

## Related

- `guides/operations/kubeswap-kubernetes` — how the wrapper works,
  lifecycle semantics, flag reference
- `cmd/kubeswap/README.md` — full flag reference and troubleshooting
- `cmd/kubeswap/chart/` — the Helm chart (values reference,
  exposure and storage options)
