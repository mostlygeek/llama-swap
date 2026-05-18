# New Router Migration TODO

This document tracks the work needed for [cmd/newrouter/main.go](../cmd/newrouter/main.go) and [internal/router/](../internal/router/) to reach feature parity with the legacy entrypoint at [llama-swap.go](../llama-swap.go) plus [proxy/proxymanager.go](../proxy/proxymanager.go).

The work is split into phases so each can land and be tested independently. Earlier phases unblock later ones.

## Current state (newrouter)

`cmd/newrouter` already supports:

- Loading config via `-config`
- Selecting Matrix vs Group router based on config
- Peer routing fallback
- Plain HTTP listen (`-listen`)
- Graceful shutdown on `SIGINT` / `SIGTERM`
- Model extraction from JSON body, query string, and form bodies (see [router.go:88](../internal/router/router.go#L88))
- `Server.ServeHTTP` dispatches a single request to peer or local router based on the requested model

Everything below is missing or only partially implemented.

---

## Phase 1 — Package relocation

Goal: move shared infrastructure packages out from under `proxy/` so the new router does not depend on the legacy proxy tree. This is a prerequisite for retiring `proxy/` in Phase 9.

- [x] Move `proxy/config` → `internal/config`
- [x] Move `proxy/configwatcher` → `internal/watcher`
- [x] Update import paths everywhere the packages are used (33 files across `cmd/`, `proxy/`, `internal/`, and `llama-swap.go`)
- [x] Verify `make test-all` still passes after the move
- [x] No behavior change in this phase — pure relocation + import rewrite

Notes:

- Both packages already have no proxy-specific dependencies, so the move is mechanical.
- Doing this first lets later phases write `internal/config` / `internal/watcher` imports directly without a follow-up rename pass.
- Fixed pre-existing data race in `internal/router/server.go` (`wg.Wait()` was deferred after `errors.Join` read the results slice).

---

## Phase 2 — Server lifecycle parity

Goal: make `cmd/newrouter` a drop-in replacement for the legacy binary's process model, _without_ yet adding any extra HTTP endpoints.

- [ ] CLI flags
  - `-tls-cert-file` / `-tls-key-file` with mutual-requirement validation
  - `-version` print + exit (wire up `version` / `commit` / `date` ldflags vars)
  - `-watch-config` to enable file-change reloads
  - Default listen address `:8080` (HTTP) or `:8443` (HTTPS) when `-listen` is empty
- [ ] `SIGHUP` triggers config reload
- [ ] Config file watcher (poll-based, `internal/watcher` after Phase 1) wired to reload when `-watch-config` is set
- [ ] Hot reload semantics:
  - Build a new `router.Server`, swap into `http.Server.Handler`, then shut down the old one
  - Guarded by a "reload in progress" mutex so overlapping signals collapse
  - Emit `ConfigFileChangedEvent` with `ReloadingState` after a short delay so UIs can refresh
- [ ] Profiles deprecation warning (matches [llama-swap.go:54](../llama-swap.go#L54))
- [ ] Skip `GIN_MODE` as the new router will only use the go standard http library

---

## Phase 3 — Logging parity

Goal: match the legacy three-logger model so the UI log stream and stdout behavior are identical.

- [ ] Mux logger (`muxLogger`) plus separate `proxyLogger` and `upstreamLogger`
- [ ] Implement `config.LogToStdout` modes: `none`, `proxy`, `upstream`, `both` (currently newrouter always writes proxy+upstream to stdout)
- [ ] `LogTimeFormat` parsing (RFC3339, kitchen, etc. — see [llama-swap.go:150](../llama-swap.go#L150) or [proxy/proxymanager.go:150](../proxy/proxymanager.go#L150))
- [ ] Deprecation warning for `LogRequests` config key
- [ ] Per-request access log middleware (`Request <ip> "<method> <path> <proto>" <status> <bytes> "<ua>" <duration>`)
- [ ] Log buffer history exposed via `OnLogData` callback and `GetHistory()` — required by Phase 7's SSE stream

---

## Phase 4 — Core HTTP endpoints

Goal: serve real LLM traffic. This phase adds the endpoint surface that `proxymanager.setupGinEngine()` defines.

The current newrouter `Server.ServeHTTP` accepts any path and routes by model. That works for `POST /v1/chat/completions`-style requests, but several endpoints need bespoke handling. Decide early whether to keep a path-agnostic dispatcher or introduce a router (chi/gorilla/std `http.ServeMux`).

- [ ] OpenAI-compatible POST endpoints (JSON body, model in body):
  - `/v1/chat/completions`, `/v1/responses`, `/v1/completions`
  - `/v1/messages`, `/v1/messages/count_tokens` (Anthropic)
  - `/v1/embeddings`
  - `/reranking`, `/rerank`, `/v1/rerank`, `/v1/reranking`
  - llama.cpp `/infill`, `/completion`
- [ ] Versionless aliases under `/v/...` (issue #728) — strip the `/v` prefix before forwarding
- [ ] Audio endpoints:
  - `POST /v1/audio/speech` (JSON)
  - `POST /v1/audio/voices` (JSON), `GET /v1/audio/voices` (model in query)
  - `POST /v1/audio/transcriptions` (multipart form)
- [ ] Image endpoints:
  - `POST /v1/images/generations` (JSON)
  - `POST /v1/images/edits` (multipart form)
- [ ] sd.cpp endpoints: `/sdapi/v1/txt2img`, `/sdapi/v1/img2img` (JSON), `GET /sdapi/v1/loras` (query)
- [ ] `GET /v1/models` listing — local models + peer models, with aliases and metadata (see [proxymanager.go:588](../proxy/proxymanager.go#L588))
- [ ] `GET /health` and `GET /wol-health`
- [ ] `GET /favicon.ico`
- [ ] `GET /` → redirect to `/ui`
- [ ] Multipart form re-encoding for upstream forwarding (see [proxymanager.go:908](../proxy/proxymanager.go#L908))

### Phase 4a — Request-body filters

Tied to the JSON handler; pulls config out of `ModelConfig.Filters`:

- [ ] `UseModelName` rewrite (issue #69)
- [ ] `StripParams` removal (issue #174)
- [ ] `SetParams` injection (issue #453)
- [ ] `SetParamsByID` per-alias overrides

### Phase 4b — Auth & CORS

- [ ] API key middleware accepting `Authorization: Bearer`, `Authorization: Basic`, and `x-api-key`; strip these headers before upstream
- [ ] OPTIONS preflight handler with `SanitizeAccessControlRequestHeaderValues` (see [proxy/sanitize_cors.go](../proxy/sanitize_cors.go))
- [ ] `Access-Control-Allow-Origin` echo on `/v1/models` when `Origin` is set

---

## Phase 5 — Upstream passthrough

- [ ] `GET /upstream` → redirect to `/ui/models`
- [ ] `ANY /upstream/<model>/<path>` proxy with multi-segment model name resolution (see `findModelInPath` at [proxymanager.go:673](../proxy/proxymanager.go#L673))
- [ ] Canonical-form redirect: `/upstream/model` → `/upstream/model/` using 301 (GET/HEAD) or 308 (other methods) to preserve method
- [ ] Path rewrite before forwarding (strip `/upstream/<model>` prefix)

---

## Phase 6 — Operations endpoints

- [ ] `GET /unload` — stop all processes (`StopImmediately`)
- [ ] `GET /running` — JSON list of ready processes with `model`, `state`, `cmd`, `proxy`, `ttl`, `name`, `description`. Matrix and group implementations differ — see [proxymanager.go:1167](../proxy/proxymanager.go#L1167)
- [ ] `GET /logs` and `GET /logs/stream[/:logMonitorID]` — historical + live tail of proxy/upstream logs (handlers in [proxy/proxymanager_loghandlers.go](../proxy/proxymanager_loghandlers.go))
- [ ] Startup preload hook (`Hooks.OnStartup.Preload`) — fires `GET /` against each model in the background; emits `ModelPreloadedEvent`

---

## Phase 7 — Metrics, perf, and SSE

- [ ] Wire `perf.Monitor` based on `config.Performance`; start/stop it with the server lifecycle
- [ ] `GET /metrics` — Prometheus output from `perf.Monitor.MetricsHandler()`
- [ ] `metricsMonitor` (activity log) integration:
  - Wrap proxy handlers with `wrapHandler(modelID, ..., captureFields, next)` to capture request/response bytes per endpoint
  - Per-endpoint `captureFields` configuration matches the legacy table in [proxymanager.go:339](../proxy/proxymanager.go#L339)
- [ ] In-flight request counter + `InFlightRequestsEvent` emission
- [ ] `/api` group (API-key protected):
  - `POST /api/models/unload`
  - `POST /api/models/unload/*model`
  - `GET /api/events` — SSE stream of `modelStatus` / `logData` / `metrics` / `inflight` envelopes
  - `GET /api/metrics`
  - `GET /api/performance` (with `?after=` RFC3339 filter)
  - `GET /api/version`
  - `GET /api/captures/:id`
- [ ] Event subscriptions for the SSE handler: `ProcessStateChangeEvent`, `ConfigFileChangedEvent`, `ActivityLogEvent`, `InFlightRequestsEvent`, log-data callbacks

---

## Phase 8 — UI serving

- [ ] Embed the React/Svelte build (see `proxy/ui_embed.go` / `proxy/ui_compress.go`)
- [ ] `GET /ui/*filepath` with brotli/gzip-aware `ServeCompressedFile`
- [ ] SPA fallback to `index.html` for non-file paths under `/ui`

---

## Phase 9 — Cutover

- [ ] Swap `llama-swap.go` to delegate to `cmd/newrouter` (or rename newrouter to be the primary entrypoint)
- [ ] Update `Makefile` build targets
- [ ] Update docs / README references to the legacy binary
- [ ] Remove `proxy/proxymanager*.go` and `gin-gonic` dependency once nothing imports them
- [ ] Run `make test-all` and confirm concurrency suite still passes against the new entrypoint

---

## Cross-cutting concerns to keep in mind

- **Single body read**: legacy and newrouter both buffer the request body once. When adding filters (Phase 4a), make sure the buffered bytes flow through `Content-Length` / `transfer-encoding` cleanup as in [proxymanager.go:872](../proxy/proxymanager.go#L872).
- **Streaming flag in context**: legacy stashes `streaming` and `model` under `proxyCtxKey`. The new router uses `ModelKey` / `ModelIDKey` — pick one set of keys and use them consistently for metrics + log handlers.
- **Matrix vs Group divergence**: any handler that calls `swapProcessGroup` or `findGroupByModelName` in the legacy needs a matrix branch too. The new router's `Router` interface already abstracts this — preserve that abstraction rather than reintroducing the branch in every handler.
- **Shutdown ordering**: `httpServer.Shutdown` must drain inflight requests _before_ `Server.Shutdown` tears down processes, otherwise inflight requests 502. Current newrouter ordering at [main.go:87](../cmd/newrouter/main.go#L87) is correct — keep it.
