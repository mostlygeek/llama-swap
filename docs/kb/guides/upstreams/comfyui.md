---
title: ComfyUI with the /comfyui endpoint
summary: Name a model comfyui_auto and use /comfyui/ so ComfyUI's websocket does not block model swapping.
category: guides
tags: [comfyui, image, websocket, upstream, swapping, ttl]
config_keys: [models.*.compat.ignoreWebsockets, models.*.concurrencyLimit, models.*.checkEndpoint, models.*.ttl, models.*.unlisted, upstream.ignorePaths]
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
    cmd: |
      python /opt/ComfyUI/main.py
      --port ${PORT}
      --listen 127.0.0.1
    # ComfyUI has no /health; its root returns 200 once the server is up
    checkEndpoint: /
    ttl: 600
    unlisted: true   # it is not an OpenAI model, keep it out of /v1/models
```

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
  comfy-second:
    cmd: python /opt/ComfyUI2/main.py --port ${PORT}
    checkEndpoint: /
    concurrencyLimit: 50
    compat:
      ignoreWebsockets: true

upstream:
  ignorePaths:
    - '.*\.(js|json|css|png|gif|jpg|jpeg|ico|txt)$'
    # ComfyUI polls these while the UI is open; do not swap for them
    - ^\/ws$|^\/api\/jobs$
```

`upstream.ignorePaths` applies to `/upstream/` only, never to `/comfyui/`,
which has the stricter root-only rule instead. Ignored paths refuse with a 409
when the model is not loaded rather than triggering a swap.

## Related

- `guides/connectivity/upstream-passthrough` — the general passthrough endpoint
- `guides/model-runtime/ttl-and-unloading` — TTL and unload behaviour
- `guides/routing/capacity-and-queues` — what `concurrencyLimit` does
