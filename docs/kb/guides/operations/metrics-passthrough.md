---
title: Passing model metrics through /metrics
summary: How to scrape a model's upstream Prometheus metrics and serve them on llama-swap's /metrics, namespaced by model name, without advancing the model's TTL.
category: guides
tags: [metrics, prometheus, monitoring, scrape, passthrough, ttl]
config_keys: [metricsPass.every, metricsPass.visibility, models.*.metricsPass.path, models.*.metricsPass.every]
updated: 2026-10-26
---

# Passing model metrics through /metrics

By default `GET /metrics` returns only llama-swap's own system and GPU stats.
With `metricsPass`, it also returns a model's **upstream** Prometheus metrics
(for example llama-server's own `/metrics`), namespaced with the model name as
a metric-name prefix. A single scrape of llama-swap then covers llama-swap and
every configured model at once.

## Why the scrape is decoupled from the request

Reading `/metrics` must not keep a model awake. If llama-swap routed a request
through the model just to read its metrics, it would reset the model's idle
`ttl` window and occupy a concurrency slot. Instead, a background poller scrapes
the model's `proxy` address **directly** on its own cadence and caches the last
good body. `GET /metrics` only reads that cache, so it never touches a model's
`lastUse`. A failed scrape keeps the previous body, so a flapping upstream does
not drop data.

## Configuration

```yaml
# Global defaults (optional; these are the defaults).
metricsPass:
  every: 15s        # default scrape cadence (minimum 1s)
  visibility: loaded # loaded = hide after unload, always = keep last scrape

models:
  banana:
    cmd: llama-server --model banana.gguf
    proxy: "http://127.0.0.1:${PORT}"
    metricsPass:
      path: /metrics   # required to enable passthrough for this model
      every: 30s       # optional; 0 uses the global cadence
```

- `metricsPass.path` (per model) is the upstream endpoint to scrape. Passthrough
  is enabled only when it is set.
- `metricsPass.every` (per model) overrides the global cadence.
- `metricsPass.visibility` controls when the section is shown:
  - `loaded` (default) - shown only while the model is running.
  - `always` - the last successful scrape is kept after the model unloads.

## Output shape

A metric the upstream reports as `llama_server_tokens_generated_total` is served
on llama-swap's `/metrics` as `banana_llama_server_tokens_generated_total`.
The model name becomes a prefix, so a model id with a slash (`author/model`)
becomes `author_model`. `HELP` and `TYPE` lines are rewritten the same way. The
model's own labels are left untouched.

## What goes wrong

- **Two model ids that map to the same prefix** collide. After slash/invalid
  character replacement, `a/b` and `a_b` both become `a_b`. Pick model ids that
  stay distinct after that replacement.
- **A model that never becomes ready** is never scraped, so its section never
  appears under `visibility: loaded`. Under `always` it appears only after the
  first successful scrape.
- **A short cadence** scrapes more often than the upstream updates, so the
  section can look stale between its own refreshes. Set `every` at or above the
  upstream's refresh interval.
