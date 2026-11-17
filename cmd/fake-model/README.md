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

It also reports a full usage and timings block on every reply, so llama-swap's
activity columns (Prompt, Generated, Cached, Prefill, Decode, Duration, Drafted)
are all populated, not just the token counts. It is a test helper: the text is
lorem ipsum and it ignores the prompt.

## Build

```bash
go build -o fake-model ./cmd/fake-model
```

## Usage

```bash
./fake-model -listen localhost:9898 -tokens 200 -tps 50 -prompt-tokens 100 -prefill-tps 3000 -load 5s
```

| flag | default | meaning |
| --- | --- | --- |
| `-listen` | `localhost:9898` | address to listen on |
| `-tokens` | `1000` | completion (output) tokens in every reply |
| `-tps` | `75` | completion (output) tokens per second; sets the decode pace |
| `-prompt-tokens` | `100` | whole prompt reported, cached tokens included (Prompt column, and Prefill) |
| `-cache-tokens` | `0` | how many prompt tokens are cached; they take no prefill time (Cached column) |
| `-prefill-tps` | `2000` | simulated prompt (prefill) tokens per second (Prefill column) |
| `-draft-tokens` | `0` | speculative-decoding draft tokens (Drafted column; 0 omits it) |
| `-draft-accepted` | `0` | speculative-decoding accepted draft tokens (Draft Accepted column) |
| `-load` | `0s` | simulated load time, e.g. `2s`, `500ms`. `/health` returns 503 until it passes |

Endpoints:

- `GET /health`: 200 once loading finishes, 503 before that.
- `POST /v1/chat/completions`: streams server-sent events when the request has
  `"stream": true` (a role chunk, one word per tick, then a final chunk carrying
  the usage/timings, then `data: [DONE]`), otherwise waits
  `(prompt-tokens - cache-tokens) / prefill-tps + tokens / tps` seconds and returns one JSON
  response. A request that arrives while the server is still loading waits
  until it is ready. Every other path returns 404.

Every reply includes an OpenAI-style `usage` object (`prompt_tokens`,
`completion_tokens`, `total_tokens`, and `prompt_tokens_details.cached_tokens`
when `-cache-tokens` is set) and a llama-server-style `timings` object
(`prompt_n`, `predicted_n`, `cache_n`, `prompt_per_second`,
`predicted_per_second`, `prompt_ms`, `predicted_ms`, and `draft_n` /
`draft_n_accepted` when `-draft-tokens` is set). The counts, rates and durations
are consistent with one another, as a real backend's would be.

```bash
curl localhost:9898/v1/chat/completions \
  -d '{"model":"fake-model","stream":true,"messages":[{"role":"user","content":"hi"}]}'
```

## Usage in llama-swap

```yaml
models:
  fake-slow:
    cmd: ./fake-model -listen localhost:${PORT} -load 10s -tokens 300 -tps 30 -prompt-tokens 120 -cache-tokens 60
  fake-fast:
    cmd: ./fake-model -listen localhost:${PORT} -tps 200 -prefill-tps 4000
```

The default `/health` check passes once `-load` has elapsed, so llama-swap shows
the model as loading for that long.

To exercise the Cached, Prefill and Drafted columns, set `-cache-tokens`,
`-prefill-tps`/`-prompt-tokens` and `-draft-tokens`/`-draft-accepted` respectively.

See also the knowledge base article `docs/kb/guides/tools/fake-model.md`.
