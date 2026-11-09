---
title: Observability, storage and Activity
summary: Use logs, metrics, captures and the Activity view to diagnose requests and retain useful history.
category: guides
tags: [operations, logs, metrics, activity, captures, stdout, http, realtime, websocket, opentelemetry, otlp]
config_keys: [logLevel, logToStdout, metricsMaxInMemory, captureBuffer, store.path, store.persistCaptures]
updated: 2026-11-09
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

## Hipfire request metrics

Hipfire is detected automatically from `owned_by: hipfire` in `/v1/models`.
No metrics configuration is needed: JSON and streaming responses use the API
usage counts and `timings.prefill_tok_s` / `timings.decode_tok_s` for the
Activity view's Prefill and Decode columns. Cached prompt tokens are included
in the input count, not added a second time.

If Hipfire reports no decode rate (for example on a multi-slot route), Decode
stays unknown. `hipfire.tok_s` includes prefill time and is not used as a decode
fallback. `tau` and `cycles` are not draft/accepted token counters, so those
columns stay unknown too. Hipfire's model listing does not report context
length or tool/vision capabilities; configure those manually when needed.

## OpenTelemetry (OTLP) metrics

llama-swap can push per-request metrics that follow the OpenTelemetry GenAI
semantic conventions to an OTLP endpoint, such as an OpenTelemetry
Collector. They come from the same record as the Activity view, so requests
served by a model that has since been swapped out are still counted, even
when the activity store fails to save the row. It is
off unless an OTLP endpoint is set in the environment; there is no config
file setting:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318 \
OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=homelab \
llama-swap -config config.yaml
```

Histograms, labeled with `gen_ai.operation.name`, `gen_ai.provider.name` and
`gen_ai.request.model`:

- `gen_ai.server.request.duration` (s): every request, with `error.type` set
  to the HTTP status for 4xx/5xx. A client that hung up (499) is not an
  error. It includes model loading when the request started the model.
- `gen_ai.client.token.usage` (`{token}`): input and output tokens, split by
  `gen_ai.token.type`, when the upstream reported them.
- `gen_ai.server.time_per_output_token` (s): successful requests with a known
  decode rate (the Activity view's Decode column).

It uses the OpenTelemetry Go SDK, which reads the standard variables:
`OTEL_EXPORTER_OTLP_ENDPOINT` (`/v1/metrics` is appended for HTTP),
`OTEL_EXPORTER_OTLP_PROTOCOL` (`http/protobuf`, the default, or `grpc`),
`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` (used as-is),
`OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_COMPRESSION`,
`OTEL_EXPORTER_OTLP_CERTIFICATE`, `OTEL_SERVICE_NAME` (default `llama-swap`),
`OTEL_RESOURCE_ATTRIBUTES`, `OTEL_METRIC_EXPORT_INTERVAL` (ms, default
60000), and `OTEL_SDK_DISABLED=true` to turn it off. `OTEL_METRICS_EXPORTER`
must be unset or include `otlp`; any other value (`none`, `prometheus`, ...)
turns it off.

What goes wrong:

- The endpoint port must match the protocol: 4318 for the default
  `http/protobuf`, 4317 for `grpc`. A mismatch shows up as failed exports in
  the proxy log. `http/json` is not supported and falls back to
  `http/protobuf` with a warning.
- Totals are cumulative from llama-swap's start and survive config reloads,
  but reset on restart. A failed push is logged and retried with the totals
  at the next interval.
- `gen_ai.provider.name` is `unknown` when llama-swap could not detect the
  server behind a model (it reads `/v1/models`). llama-server shows as
  `llama.cpp`.
- Time to first token is not reported; llama-swap does not measure it.
- Realtime (`/v1/realtime`) websocket sessions are not reported: one
  session is not one request. They still show in the Activity view.
- A malformed `OTEL_RESOURCE_ATTRIBUTES` entry is skipped with a warning;
  the rest are still applied.

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
`store.persistCaptures: true` also keeps captures, in a second SQLite file
next to it named with `_captures` added to the base name:

```yaml
store:
  path: /data/metrics.db # captures go in /data/metrics_captures.db
  persistCaptures: true
captureBuffer: 2048 # MB of compressed captures to keep
```

Captures live in their own file so large blobs do not slow down the activity
log. Each activity row records the ID of its capture, such as
`26103120-a7Kq`: the UTC hour (yymmddhh) and four random characters. A row
whose capture was pruned shows no capture.

`captureBuffer` limits both in-memory and on-disk captures by their
compressed size. When a new capture pushes the total over the limit, the
oldest captures are deleted in the background. `captureBuffer: 0` turns
captures off. Stored captures stay in the file but are hidden until captures
are turned back on. SQLite reuses the space of deleted captures, so the file
grows to about `captureBuffer` and then stays that size.

What goes wrong:

- `persistCaptures` without `store.path` fails config loading.
- Moving or renaming only one of the two files breaks the pair. Move both, or
  change `store.path` and rename `metrics_captures.db` to match.
- A single capture larger than `captureBuffer` is skipped and logged as a
  warning.
- At very high request rates two requests in the same hour can draw the
  same capture ID. The second capture is skipped with a warning; the first
  is never overwritten.

## Realtime sessions

A `/v1/realtime` websocket (the Playground's Realtime tab, or any OpenAI
Realtime client) is one Activity row, written when the socket closes. Its task
is `realtime`, its status `101` and its duration the whole session. llama-swap
reads the session's events as they pass through and fills in:

- Generated: the sum of every reply's `output_tokens`.
- Decode: those tokens over the time from each reply's first output to its
  end. Prompt and Prefill stay empty; llama-server reports neither.
- Meta: `session` id, `replies`, `interrupted` (replies cancelled by talking
  over them), `audio_in` and `audio_out` in seconds, `text_messages`, and
  `errors` with the `last_error` message.

What goes wrong:

- No row appears until the session ends. A session left open has no row yet;
  it shows on the Activity page's in-flight list instead.
- A refused upgrade, such as a model that fails to start, is recorded as an
  ordinary failed request with its error, not as a session.
- Sessions have no capture. Their traffic is mostly base64 audio.

Do not put secrets in captures or debug logs. Reduce retention after diagnosing
an issue.
