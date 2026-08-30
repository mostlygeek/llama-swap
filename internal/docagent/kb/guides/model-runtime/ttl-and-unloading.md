---
title: Automatic model unloading with ttl
summary: How ttl, globalTTL, unloadTimeout and Docker cmdStop interact, plus how to unload on demand.
category: guides
tags: [ttl, unload, vram, memory, idle, docker, container, cmd-stop]
config_keys: [globalTTL, unloadTimeout, models.*.ttl, models.*.unloadTimeout, models.*.cmdStop]
updated: 2026-08-25
---

# Automatic model unloading with ttl

By default llama-swap keeps a model loaded until something else needs the GPU.
TTL frees the VRAM after a period of inactivity instead.

Docker needs one extra setting: use `cmdStop: docker stop ${MODEL_ID}` so an
unload stops the container, not only the local `docker run` client process.

## The two settings

```yaml
# global default, in seconds. 0 = never unload automatically
globalTTL: 0

models:
  qwen-coder:
    ttl: 300      # unload after 5 minutes idle
    cmd: llama-server --port ${PORT} -m /models/qwen.gguf
```

`models.*.ttl` values and what they mean:

| value | behaviour |
| --- | --- |
| `-1` (default) | inherit `globalTTL` |
| `0` | never unload automatically |
| `> 0` | unload after this many seconds of inactivity |

So `globalTTL: 600` with no per-model `ttl` unloads every model after 10
minutes idle. A model that should stay resident sets `ttl: 0` to opt out.

The timer measures **inactivity**, not lifetime — it resets on every request.

## `unloadTimeout` is a different thing

`ttl` decides *when* to unload. `unloadTimeout` decides *how long to wait* for
the process to exit once unloading starts.

```yaml
unloadTimeout: 10        # global default, seconds

models:
  docker-llama:
    unloadTimeout: 30    # docker stop is slow
```

It applies to every unload — TTL expiry, a manual unload, or a swap. A model
whose `unloadTimeout` is `0` uses the global value.

Raise it for anything slow to shut down: containers, vLLM, anything with a
`cmdStop` that talks to another daemon. Too low and llama-swap force-kills a
process mid-shutdown, which can leave a container running and its VRAM held.

For Docker, set `cmdStop: docker stop ${MODEL_ID}`. llama-swap otherwise stops
the local Docker client process, which can leave the container running. A
ten-minute idle timeout for a container therefore needs both settings:

```yaml
models:
  docker-llama:
    ttl: 600
    cmdStop: docker stop ${MODEL_ID}
    unloadTimeout: 30
```

## Unloading on demand

```console
$ curl http://localhost:8080/unload                        # everything
$ curl -X POST http://localhost:8080/api/models/unload      # everything
$ curl -X POST http://localhost:8080/api/models/unload/qwen-coder
```

The web UI's model list has an unload button per model that hits the same
endpoint.

## Picking a value

- **Single GPU, one model at a time.** TTL buys you little — swapping already
  evicts the old model. `globalTTL: 0` is fine.
- **Several models resident together** (see `guides/routing/groups-and-matrix`). TTL is how
  you reclaim VRAM from the ones that have gone quiet. Something in the 300–900
  second range is a reasonable start.
- **Slow-loading models** (large weights, vLLM). A short TTL is expensive — you
  pay a long cold start on the next request. Give them a long TTL or `ttl: 0`
  and let the router evict them only when it must.
- **Keeping a small model always warm.** `ttl: 0` plus a `persistent` group.

## Related

- `reference/config/globalTTL` and `reference/config/unloadTimeout`
- `guides/routing/groups-and-matrix` — controlling what runs together
