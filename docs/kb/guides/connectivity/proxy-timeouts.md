---
title: Proxy and model startup timeouts
summary: Distinguish client proxy timeouts from health-check and model startup failures, and fix 502 EOF errors after streams.
category: guides
tags: [proxy, timeout, health-check, loading, keep-alive, 502, eof]
config_keys: [healthCheckTimeout, models.*.proxy, models.*.checkEndpoint, models.*.timeouts.idleConn, models.*.disableKeepAlives]
updated: 2026-10-07
---

# Proxy and model startup timeouts

`healthCheckTimeout` limits how long llama-swap waits for a newly started model
to become healthy. `proxy` must point to the model server that llama-swap can
reach, and `checkEndpoint` must return success there.

If a request times out after the process starts, verify the proxy URL and
endpoint from the llama-swap host first. Raising a timeout only hides a wrong
port, Docker mapping, or unavailable health endpoint.

## 502 "proxy error: EOF" right after a stream

llama-swap keeps idle upstream connections open and reuses them for the next
request; `timeouts.idleConn` sets how long an idle one is kept. Some upstreams,
llama-server among them, advertise keep-alive on a streamed response and then
close the connection. A request sent right after the stream can land on the
closed connection and fail with a 502 `proxy error: EOF`. Retrying succeeds,
because the retry opens a new connection.

Turn off connection reuse for that model:

```yaml
models:
  my-model:
    cmd: llama-server --port ${PORT} -m model.gguf
    disableKeepAlives: true
```

Every request then opens a new connection, so `timeouts.idleConn` no longer
does anything for that model. The extra connect costs well under a millisecond
on localhost; leave it off for upstreams that do not show the error.
