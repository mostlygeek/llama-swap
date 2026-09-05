---
title: Observability, storage and Activity
summary: Use logs, metrics, captures and the Activity view to diagnose requests and retain useful history.
category: guides
tags: [operations, logs, metrics, activity, captures]
config_keys: [logLevel, logToStdout, metricsMaxInMemory, captureBuffer, store.captureDir, store.captureMaxMB]
updated: 2026-09-05
---

# Observability, storage and Activity

Use the Activity view for recent request timing and model events, logs for
process and proxy failures, and metrics for trends. `metricsMaxInMemory` and
`captureBuffer` bound retained in-memory data; increase them only when the
memory cost is acceptable.

```yaml
logLevel: debug
logToStdout: true
metricsMaxInMemory: 1000
captureBuffer: 100
```

By default request/response captures live only in memory (bounded by
`captureBuffer`) and are lost on restart. To keep them across restarts, enable
an on-disk capture tier under `store` alongside a persistent database path:

```yaml
store:
  path: /var/lib/llama-swap/llama-swap.sqlite
  captureDir: /var/lib/llama-swap/captures   # setting this opts in
  captureMaxMB: 512                          # 0 (default) = unlimited disk usage
```

The on-disk tier is a second layer behind the in-memory one: recent captures are
served from memory, older ones (or those too large for the memory buffer) are
read back from disk. Set `captureDir` to opt in — an empty `captureDir` (default)
stores nothing on disk. `captureMaxMB` caps the directory size and prunes the
oldest captures when exceeded; `0` (default) means unlimited disk usage. It also
requires a persistent `store.path` because captures are keyed by activity IDs
that reset on every in-memory boot.


Do not put secrets in captures or debug logs. Reduce retention after diagnosing
an issue.
