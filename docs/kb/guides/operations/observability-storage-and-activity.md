---
title: Observability, storage and Activity
summary: Use logs, metrics, captures and the Activity view to diagnose requests and retain useful history.
category: guides
tags: [operations, logs, metrics, activity, captures, stdout, http]
config_keys: [logLevel, logToStdout, metricsMaxInMemory, captureBuffer, store.path, store.captures, store.captures.maxSizeMB, store.captures.maxCaptureMB]
updated: 2026-10-05
---

# Observability, storage and Activity

Use the Activity view for recent request timing and model events, logs for
process and proxy failures, and metrics for trends. `metricsMaxInMemory` and
`captureBuffer` bound retained in-memory data; increase them only when the
memory cost is acceptable. `store.captures` keeps captures on disk instead, under
a disk budget. See [Persistent captures](#persistent-captures).

```yaml
logLevel: debug
logToStdout: "proxy,http"
metricsMaxInMemory: 1000
captureBuffer: 100
```

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

## Persistent captures

`captureBuffer` holds captures in RAM: they are gone after a restart and their
ceiling is a memory ceiling. `store.captures` writes them to a SQLite file of
their own, so they survive restarts and are bounded by disk space.

```yaml
store:
  path: /var/lib/llama-swap/activity.sqlite
  captures:
    path: /var/lib/llama-swap/captures.sqlite
    maxSizeMB: 500
    maxCaptureMB: 10
```

Captures are keyed by the Activity row they belong to, so the Activity page
marks and opens them exactly as it does for in-memory captures.

- `store.captures.path` must be a different file than `store.path`, and
  `store.path` must be set: captures point at activity rows, and an in-memory
  activity log is pruned.
- `maxSizeMB` is the whole budget. Once it is exceeded, the oldest captures are
  evicted to make room.
- `maxCaptureMB` skips a single capture that compressed above the limit, so one
  oversized multimodal response cannot evict the history around it.
- `store.captures` supersedes `captureBuffer` and `metricsMaxInMemory`. Naming
  either one alongside it is a config error: llama-swap refuses to start rather
  than guess which setting you meant.

Do not put secrets in captures or debug logs. Captures redact authorization
headers, but request bodies are stored as sent, and they are not encrypted.
Persistent captures outlive the process, so delete the captures file once you
are done diagnosing. Reduce retention after diagnosing an issue.
