# Generate synthetic request data with fake-model

`fake-model` (see `docs/kb/guides/tools/fake-model.md`) returns canned text at
a fixed speed and never loads a model. That makes it a cheap, deterministic way
to generate **synthetic request data**: rows in the Activity table, in-flight
requests, swapping/queueing events, and streaming behavior at a known tokens per
second — no GPU, no model download, sub-second startup.

## Build

```bash
go build -o build/fake-model ./cmd/fake-model
go build -o build/llama-swap .
```

## Minimal config

Save this as `build/test-config.yaml`. Two models at different speeds give you a
spread of durations and token counts in the same table.

```yaml
logToStdout: "proxy,http"
startPort: 10200

models:
  fake-fast:
    name: "Fake Fast"
    cmd: ./build/fake-model -listen localhost:${PORT} -tokens 120 -tps 200
  fake-slow:
    name: "Fake Slow"
    cmd: ./build/fake-model -listen localhost:${PORT} -tokens 300 -tps 30
```

`-tokens` is the word count of every reply and `-tps` sets the stream pace. A
request first sleeps through a simulated prefill, so it takes roughly
`prompt-tokens / prefill-tps + tokens / tps` seconds. Omit `-load` to start
serving instantly.

## Start and generate traffic

```bash
./build/llama-swap -config build/test-config.yaml -listen :8080
```

```bash
# several fast requests (each ~0.6s), fired in parallel
for i in 1 2 3 4 5; do
  curl -s -o /dev/null localhost:8080/v1/chat/completions \
    -H 'Content-Type: application/json' \
    -d '{"model":"fake-fast","messages":[{"role":"user","content":"hi"}]}' &
done
wait

# one slow request (~10s) and one streaming request
curl -s -o /dev/null localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"fake-slow","messages":[{"role":"user","content":"story"}]}'
curl -s -o /dev/null localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"fake-fast","stream":true,"messages":[{"role":"user","content":"hi"}]}'
```

Check the Activity page, or `curl localhost:8080/api/metrics/stats` for the
aggregate counts. Each completed request produces one Activity row with its model,
status code, token counts, and real wall-clock duration.

## What you get, and what you don't

- **Status 200 rows** with `prompt`/`generated` token counts and real `duration`
  values, enough to fill and exercise the Activity table and its summary
  aggregate figures.
- **Prefill and Decode are populated.** `fake-model` reports
  `prompt_per_second` and `predicted_per_second` in its timings block, so the
  Prefill and Decode columns show the configured `-prefill-tps` and `-tps`
  rates. To fill Cached and Drafted too, add `-cache-tokens` and
  `-draft-tokens`/`-draft-accepted`.
- **One upstream process per model id.** All requests for a given model share
  that one `fake-model` process, and it serves them concurrently, so a busy
  model does not queue by itself. A model serves up to `concurrencyLimit`
  requests at a time (default 10) and anything beyond that is refused with
  HTTP 429 — set `concurrencyLimit: 1` to exercise that path.
- **TTL unloading needs `ttl`.** Add `ttl: 10` to a model to watch it unload
  after that many idle seconds and swap back in on the next request.
- **Loading state.** With `-load` set, the model reports 503 from `/health`
  until the delay elapses, so requests can arrive "while loading" and the UI
  shows the loading state for that long.
- **Determinism is the point.** The text is lorem ipsum and the prompt is
  ignored; the same flags always produce the same token count and pace, so a
  test's screenshots and numbers are reproducible.
