---
title: ComfyUI with the /comfyui endpoint
summary: Name a model comfyui_auto and use /comfyui/ so ComfyUI's websocket does not block model swapping.
category: guides
tags: [comfyui, image, websocket, upstream, swapping, ttl, docker]
config_keys: [models.*.compat.ignoreWebsockets, models.*.concurrencyLimit, models.*.checkEndpoint, models.*.cmdStop, models.*.unloadTimeout, models.*.ttl, models.*.unlisted, upstream.ignorePaths]
updated: 2026-09-19
---

# ComfyUI with the /comfyui endpoint

ComfyUI is a web app, not an OpenAI-compatible server, so it does not swap well
through the normal endpoints. Its browser frontend holds a websocket open for
as long as the tab is open. Counted as a normal request, that connection makes
the model look permanently busy and llama-swap will not swap it out.

`/comfyui/` is a dedicated passthrough that applies the needed settings for
you. It is wired to one reserved model ID: `comfyui_auto`.

## Setup

Name the model exactly `comfyui_auto` and point a browser at
`http://localhost:8080/comfyui/`.

```yaml
models:
  comfyui_auto:
    # ComfyUI has no /health; its root returns 200 once the server is up
    checkEndpoint: /
    cmdStop: docker stop comfyui-auto
    cmd: >
      docker run --rm
      --name comfyui-auto --runtime=nvidia
      --gpus '"device=2,3"' -p ${PORT}:8188
      -v /path/to/comfyui/storage-cache/dot-cache:/root/.cache
      -v /path/to/comfyui/storage-cache/dot-config:/root/.config
      -v /path/to/comfyui/storage-nodes/dot-local:/root/.local
      -v /path/to/comfyui/storage-nodes/custom_nodes:/root/ComfyUI/custom_nodes
      -v /path/to/comfyui/storage-models/models:/root/ComfyUI/models
      -v /path/to/comfyui/storage-models/hf-hub:/root/.cache/huggingface/hub
      -v /path/to/comfyui/storage-models/torch-hub:/root/.cache/torch/hub
      -v /path/to/comfyui/storage-user/input:/root/ComfyUI/input
      -v /path/to/comfyui/storage-user/output:/root/ComfyUI/output
      -v /path/to/comfyui/storage-user/user-profile:/root/ComfyUI/user
      -v /path/to/comfyui/storage-user/user-scripts:/root/user-scripts
      -e CLI_ARGS=""
      yanwk/comfyui-boot:cu130-slim-v2
    unloadTimeout: 30   # docker stop is slow
    ttl: 600
    unlisted: true      # it is not an OpenAI model, keep it out of /v1/models
```

`cmdStop` matters here: without it llama-swap stops the local `docker run`
client and the container keeps the GPU. See
`guides/model-runtime/ttl-and-unloading`.

Nothing else is required. `/comfyui` (no trailing slash) redirects to
`/comfyui/` and keeps the query string, so `?token=...` style links survive.
API key checks apply exactly as they do on other endpoints.

## What llama-swap forces on comfyui_auto

Two settings are applied while the config loads and cannot be lowered:

| setting | value | why |
| --- | --- | --- |
| `compat.ignoreWebsockets` | always `true` | the frontend websocket does not count toward concurrency, in-flight requests, TTL activity, or swap decisions |
| `concurrencyLimit` | at least `50` | one open ComfyUI tab makes many parallel requests for assets and job polling |

A higher `concurrencyLimit` in your config is kept. A lower one is raised to
50.

## Only the root path starts the model

A request to `/comfyui/` may load `comfyui_auto`. Every other path under
`/comfyui/` returns `409 Conflict` with `only /comfyui/ can start it` when the
model is not ready.

This is deliberate. After the model unloads, an open browser tab keeps
requesting assets, polling APIs, and reconnecting its websocket. Without the
rule, that background traffic would reload the model forever and nothing else
would get the GPU. Reload `/comfyui/` to start it again.

## What goes wrong

- **`404 local model comfyui_auto not found`.** The model is named something
  else, or it is defined on a peer. `/comfyui/` only serves a local model with
  that exact ID. Aliases do not work here — the lookup is by model ID.
- **A stale tab shows broken images and failed requests.** The model unloaded
  while the tab was open; those are the 409s above. Reload the page.
- **The model unloads while you are working.** Websocket traffic is ignored, so
  it does not reset the TTL timer. Watching a long render over the websocket
  counts as idle. Use a longer `ttl`, or `ttl: 0` to disable automatic
  unloading, and rely on a swap to free the VRAM.
- **ComfyUI never becomes ready.** The default `checkEndpoint` is `/health`,
  which ComfyUI does not serve. Set `checkEndpoint: /`, or `none` to skip the
  check. See `guides/model-runtime/troubleshooting-model-wont-load`.

## If you cannot use comfyui_auto

Only one ComfyUI instance can use `/comfyui/`. For a second one, or to keep
your own model ID, serve it through `/upstream/<model>/` and reproduce the
settings by hand:

```yaml
models:
  my-other-comfyui-model:
    checkEndpoint: /
    # the two settings /comfyui/ would have applied for you
    concurrencyLimit: 50
    compat:
      ignoreWebsockets: true
    cmdStop: docker stop ${MODEL_ID}
    cmd: >
      docker run --rm
      --name ${MODEL_ID} --runtime=nvidia
      --gpus '"device=2,3"' -p ${PORT}:8188
      -v /path/to/comfyui/storage-models/models:/root/ComfyUI/models
      -v /path/to/comfyui/storage-user/output:/root/ComfyUI/output
      -e CLI_ARGS=""
      yanwk/comfyui-boot:cu130-slim-v2

upstream:
  ignorePaths:
    - '.*\.(js|json|css|png|gif|jpg|jpeg|ico|txt)$'
    # ComfyUI polls these while the UI is open; do not swap for them
    - ^\/ws$|^\/api\/jobs$
```

The mounts are trimmed here; copy the full set from the block above. `cmd` is a
folded YAML scalar, so a `#` line inside it is an argument, not a comment.

Use `${MODEL_ID}` for the container name so a second instance cannot collide
with the `comfyui-auto` container, and keep the default static-asset pattern —
listing `ignorePaths` at all replaces the default instead of adding to it.

`upstream.ignorePaths` applies to `/upstream/` only, never to `/comfyui/`,
which has the stricter root-only rule instead. Ignored paths refuse with a 409
when the model is not loaded rather than triggering a swap.

## Related

- `guides/connectivity/upstream-passthrough` — the general passthrough endpoint
- `guides/model-runtime/ttl-and-unloading` — TTL and unload behaviour
- `guides/routing/capacity-and-queues` — what `concurrencyLimit` does
