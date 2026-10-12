---
title: audio.cpp UI with the /audiocpp endpoint
summary: Name a model audiocpp_auto and use /audiocpp/ to reach audio.cpp's own UI and model management without naming a model in each request.
category: guides
tags: [audiocpp, audio, tts, upstream, ui, docker]
config_keys: [models.*.cmd, models.*.checkEndpoint, models.*.ttl, models.*.unlisted]
updated: 2026-10-11
---

# audio.cpp UI with the /audiocpp endpoint

`audiocpp_server` has its own web UI and model management. `/audiocpp/` is a
passthrough to that server. It is wired to one reserved model ID,
`audiocpp_auto`, so requests do not have to say which model to use.

## Setup

Name the model exactly `audiocpp_auto` and point a browser at
`http://localhost:8080/audiocpp/`.

```yaml
models:
  audiocpp_auto:
    # audio.cpp is compiled into the unified image
    cmd: >
      docker run --rm --runtime nvidia
      -p ${PORT}:8080
      -v /mnt/nvme/audio.cpp:/models
      --entrypoint audiocpp_server
      ghcr.io/mostlygeek/llama-swap:unified-cuda-rootless
      --ui --host 0.0.0.0 --ui-management
```

`/audiocpp` (no trailing slash) redirects to `/audiocpp/` and keeps the query
string. API key checks apply as they do on other endpoints. The path after
`/audiocpp` is sent to audio.cpp unchanged, so `/audiocpp/v1/tasks/run` reaches
audio.cpp's `/v1/tasks/run`.

Unlike `comfyui_auto`, nothing else is forced on this model. It is an ordinary
model that happens to have a fixed URL.

## What goes wrong

- **`404 local model audiocpp_auto not found`.** The model is named something
  else, or it is defined on a peer. `/audiocpp/` only serves a local model with
  that exact ID. Aliases do not work here — the lookup is by model ID.
- **The page loads but model management is missing.** Start `audiocpp_server`
  with `--ui --ui-management`.
- **The page cannot be reached from the host.** The server must listen on
  `0.0.0.0` inside the container, and the container port must match
  `-p ${PORT}:8080`.
- **The model never becomes ready.** The default `checkEndpoint` is `/health`.
  Set `checkEndpoint` to a path the server answers, or `none`. See
  `guides/model-runtime/troubleshooting-model-wont-load`.
- **To use a different model ID or run more than one.** Serve it through
  `/upstream/<model>/` instead.

## Related

- `guides/connectivity/upstream-passthrough` — the general passthrough endpoint
- `guides/upstreams/comfyui` — the same pattern for ComfyUI
