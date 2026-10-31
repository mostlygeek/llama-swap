---
title: Observability, storage and Activity
summary: Use logs, metrics, captures and the Activity view to diagnose requests and retain useful history.
category: guides
tags: [operations, logs, metrics, activity, captures, stdout, http]
config_keys: [logLevel, logToStdout, metricsMaxInMemory, captureBuffer, store.path, store.captures]
updated: 2026-10-31
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

## Keeping activity and captures across restarts

By default activity rows and request/response captures are kept in memory and
lost on restart. `store.path` keeps activity rows in a SQLite file.
`store.captures` keeps captures in a second SQLite file:

```yaml
store:
  path: /data/activity.db
  captures: /data/captures.db
captureBuffer: 2048 # MB of compressed captures to keep
```

`captureBuffer` limits both in-memory and on-disk captures. When a new
capture pushes the total over the limit, the oldest captures are deleted.
`captureBuffer: 0` turns captures off. Stored captures stay in the file but
are hidden until captures are turned back on. SQLite reuses the space of
deleted captures, so the file grows to about `captureBuffer` and then stays
that size.

What goes wrong:

- `store.captures` without `store.path` fails config loading. Each capture is
  linked to an activity row by ID, so the activity rows have to persist too.
- `store.captures` set to the same file as `store.path` fails config loading.
- llama-swap refuses to start when the captures file does not match the
  activity file, for example after `store.path` was pointed at a new file or
  the activity file was replaced. The error says the captures database is out
  of sync. Point `store.captures` back at the file that was created with that
  activity database, or move the captures file aside to start a new one.
- A single capture larger than `captureBuffer` is skipped and logged as a
  warning.

Do not put secrets in captures or debug logs. Reduce retention after diagnosing
an issue.
