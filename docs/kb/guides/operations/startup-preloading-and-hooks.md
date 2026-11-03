---
title: Startup preloading and hooks
summary: Load selected models at startup, restore default models after others unload, and run hooks around lifecycle events.
category: guides
tags: [startup, preload, restore, default model, hooks, operations]
config_keys: [hooks, hooks.on_startup.preload, hooks.on_unload.restore, hooks.on_unload.delay, models.*.ttl]
updated: 2026-11-03
---

# Startup preloading and hooks

Use startup hooks when an external action must run as llama-swap starts, and
preload only models your machine can keep resident. A model with `ttl: 0` stays
loaded after it has been requested; preloading does not make an incompatible
model fit in memory.

## Keep a default model loaded

`on_startup.preload` loads a model once. When another model later swaps it
out, it stays unloaded until a request asks for it again. `on_unload.restore`
brings it back on its own:

```yaml
hooks:
  on_startup:
    preload: [main-llm, helper]
  on_unload:
    restore: [main-llm, helper]
    delay: 30

models:
  main-llm: { cmd: ..., }          # no ttl: the default
  experiment: { cmd: ..., ttl: 900 } # swaps main-llm out, unloads when idle
```

After any model stops (unload, ttl, eviction or crash) and no other model has
stopped for `delay` seconds (default 30), each `restore` model that is not
running is loaded, in order — **but only if loading it evicts nothing**. A
model someone requested is never displaced, and a request waiting in the queue
always goes first. So in the example, `experiment` keeps the GPU for as long
as it is used; 15 minutes after its last request it unloads and `main-llm`
comes back. Resuming paused inference also triggers a restore.

What goes wrong:

- The models you swap in need a `ttl`, otherwise they never unload and the
  default never comes back.
- A model that fails to start is not retried until another model stops, so a
  broken default does not loop. Check the proxy log for `restore:` lines.
- An unknown model name in `restore` is a configuration error.

Keep hook commands fast and observable. A failing hook or a model that cannot
become healthy should be diagnosed in logs before adding retries.
