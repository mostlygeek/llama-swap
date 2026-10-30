---
title: Observability, storage and Activity
summary: Use logs, metrics, captures and the Activity view to diagnose requests and retain useful history.
category: guides
tags: [operations, logs, metrics, activity, captures, stdout, http]
config_keys: [logLevel, logToStdout, metricsMaxInMemory, captureBuffer, models.*.metrics.promptPerSecond, models.*.metrics.tokensPerSecond]
updated: 2026-09-25
---

# Observability, storage and Activity

Use the Activity view for recent request timing and model events, logs for
process and proxy failures, and metrics for trends. `metricsMaxInMemory` and
`captureBuffer` bound retained in-memory data; increase them only when the
memory cost is acceptable.

```yaml
logLevel: debug
logToStdout: "proxy,http"
metricsMaxInMemory: 1000
captureBuffer: 100
```

## Backend token rates

For backends with different performance field names, set per-model `metrics`
paths. For an already-running Hipfire server:

```yaml
models:
  hipfire:
    proxy: http://127.0.0.1:8080
    checkEndpoint: /v1/models
    metrics:
      promptPerSecond: timings.prefill_tok_s
      tokensPerSecond: timings.decode_tok_s
```

Paths use GJSON syntax, relative to the response JSON root or each streaming
SSE `data:` object. Values must be non-negative numbers in tokens/second.
Configured rates override built-in detection independently; omit either path
to keep its built-in behavior. For streams, the last valid number wins.
This affects recorded statistics only, not the response sent to the client.

If rates are still missing, inspect the response capture: a wrong path, null,
string or negative value leaves the built-in rate unchanged. Hipfire may report
null for decode speed on execution modes that do not measure it; wall-inclusive
`hipfire.tok_s` is not a substitute for decode throughput.

## Log streams

llama-swap keeps three log streams. Each has its own tab on the UI's Logs page
and its own endpoint, `GET /logs/stream/<stream>`:

- `proxy`: llama-swap's own messages, such as model swaps, loading and errors.
- `upstream`: a copy of the upstream processes' stdout and stderr.
- `http`: one access log line per HTTP request (client, method, path, status,
  size, duration).

`logToStdout` chooses which streams are also written to stdout. It takes a
comma separated list such as `"proxy,http"` or `"upstream,http"`, or one of
`"both"` (every stream) and `"none"`. The default is `"proxy"`.

What goes wrong:

- HTTP access lines used to be part of the proxy log. With the default
  `"proxy"` they no longer reach stdout. Use `"proxy,http"` to keep them.
- A value outside the list, like `true` or `"proxy,debug"`, fails config
  loading.
- `logToStdout` is read at startup only. Restart llama-swap after changing it.

Do not put secrets in captures or debug logs. Reduce retention after diagnosing
an issue.
