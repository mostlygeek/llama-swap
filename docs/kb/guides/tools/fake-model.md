---
title: Test llama-swap without a GPU using fake-model
summary: fake-model is a tiny OpenAI-compatible server with a configurable load time and token speed, for testing swapping, loading and streaming without a real model.
category: guides
tags: [fake-model, testing, mock, load-test, streaming, health-check, tools]
config_keys: [models.*.cmd, models.*.checkEndpoint, models.*.ttl]
updated: 2026-10-23
---

# Test llama-swap without a GPU using fake-model

`fake-model` is a small server in the llama-swap repository that answers
`/v1/chat/completions` with lorem ipsum at a fixed speed. It loads no model, so
use it to try out swapping, TTL, groups, client streaming and loading feedback
without spending VRAM or waiting on a real backend. It ignores the prompt.

Build it from the repository root:

```bash
go build -o fake-model ./cmd/fake-model
```

## Flags

| flag | default | meaning |
| --- | --- | --- |
| `-listen` | `localhost:9898` | address to listen on |
| `-tokens` | `1000` | words in every reply |
| `-tps` | `75` | tokens per second for streamed replies |
| `-load` | `0s` | simulated load time (`2s`, `500ms`) |

## Config

```yaml
models:
  fake-slow:
    cmd: ./fake-model -listen localhost:${PORT} -load 10s -tokens 300 -tps 30
    ttl: 60
  fake-fast:
    cmd: ./fake-model -listen localhost:${PORT} -tps 200
```

`/health` returns 503 until `-load` has passed, then 200, so llama-swap reports
the model as loading for that long. Choose `-load` larger than
`healthCheckTimeout` to see how a model that never becomes ready is reported.

## What it does and does not do

- Streams (`"stream": true`) a role chunk, then one word per tick, then
  `data: [DONE]`. Without streaming it waits `tokens / tps` seconds and returns a
  single JSON response.
- A request that arrives during loading waits until the load finishes.
- Only `/health` and `/v1/chat/completions` exist. Anything else returns 404, so
  embeddings, `/v1/models` and similar requests through it fail.
- The reply is always the same text and the `model` field is always
  `fake-model`, whatever the request says.
- `-listen` must match the model's `proxy`. Using `${PORT}` in `cmd` keeps them
  the same (see `guides/model-runtime/writing-cmd`).
