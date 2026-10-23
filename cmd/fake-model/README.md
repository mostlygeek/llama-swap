# fake-model

`fake-model` is a tiny OpenAI-compatible server that returns canned text at a
fixed speed. It never loads a model, so it needs no GPU and starts instantly.

## Why use this?

Testing llama-swap behavior with a real model is slow and uses VRAM. With
`fake-model` you control how long "loading" takes and how fast tokens stream,
so you can reproduce:

- slow model loads, loading-progress UI, health-check timeouts
- swapping, queueing, TTL unloading and group/matrix behavior
- client streaming behavior at a known tokens per second
- config changes, without waiting on a real backend

It is a test helper. The text is lorem ipsum, and it ignores the prompt.

## Build

```bash
go build -o fake-model ./cmd/fake-model
```

## Usage

```bash
./fake-model -listen localhost:9898 -tokens 200 -tps 50 -load 5s
```

| flag | default | meaning |
| --- | --- | --- |
| `-listen` | `localhost:9898` | address to listen on |
| `-tokens` | `1000` | number of tokens (words) in every reply |
| `-tps` | `75` | tokens per second; sets the pace of streamed replies |
| `-load` | `0s` | simulated load time, e.g. `2s`, `500ms`. `/health` returns 503 until it passes |

Endpoints:

- `GET /health`: 200 once loading finishes, 503 before that.
- `POST /v1/chat/completions`: streams server-sent events when the request has
  `"stream": true`, otherwise waits `tokens / tps` seconds and returns one JSON
  response. A request that arrives while the server is still loading waits
  until it is ready. Every other path returns 404.

```bash
curl localhost:9898/v1/chat/completions \
  -d '{"model":"fake-model","stream":true,"messages":[{"role":"user","content":"hi"}]}'
```

## Usage in llama-swap

```yaml
models:
  fake-slow:
    cmd: ./fake-model -listen localhost:${PORT} -load 10s -tokens 300 -tps 30
  fake-fast:
    cmd: ./fake-model -listen localhost:${PORT} -tps 200
```

The default `/health` check passes once `-load` has elapsed, so llama-swap shows
the model as loading for that long.

See also the knowledge base article `docs/kb/guides/tools/fake-model.md`.
