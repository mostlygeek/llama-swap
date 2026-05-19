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

## Phase 1 — Package relocation -- Completed.

Goal: move shared infrastructure packages out from under `proxy/` so the new router does not depend on the legacy proxy tree. This is a prerequisite for retiring `proxy/` in Phase 10.

---

## Phase 2 — Server lifecycle parity -- Completed.

Goal: make `cmd/newrouter` a drop-in replacement for the legacy binary's process model, _without_ yet adding any extra HTTP endpoints.

---

## Phase 3 — `internal/chain` package -- Completed.

API: `chain.New(mws...).Then(final)` for ServeMux registration; `Append` returns an extended Chain without mutating the receiver, so a base stack (auth/CORS) can be reused across many routes with per-route additions.

---

## Phase 4 — `internal/server.Server` scaffolding (ProxyManager replacement)

Goal: build the structural shell of [internal/server/server.go](../internal/server/server.go) so it can stand in for [proxy.ProxyManager](../proxy/proxymanager.go#L67). This phase intentionally adds no new endpoints — it wires the dependencies and lifecycle that later phases (endpoints, metrics, SSE, UI) plug into. After this phase, `cmd/newrouter/main.go` constructs a `server.Server` instead of a bare `router.Server`, and behavior is unchanged.

The legacy `ProxyManager` collapses three concerns into one struct: the HTTP mux, the model→process router, and the cross-cutting services (loggers, metrics, perf, inflight counter, version). The new layout keeps `internal/router.Server` focused on model dispatch and lets `internal/server.Server` own everything else.

- [ ] Define the `Server` struct in [internal/server/server.go](../internal/server/server.go) with fields for:
  - `cfg config.Config`
  - `router *router.Server` (embedded model dispatcher from Phase 1/2)
  - `proxyLogger`, `upstreamLogger *logmon.Monitor` (passed in from `cmd/newrouter/main.go`)
  - `metricsMonitor *metricsMonitor` and `perfMonitor *perf.Monitor` (populated in Phase 8)
  - `inFlightCounter` (populated in Phase 8)
  - `shutdownCtx context.Context` + `shutdownCancel context.CancelFunc`
  - `buildDate`, `commit`, `version string`
- [ ] `New(cfg config.Config, proxyLog, upstreamLog *logmon.Monitor) (*Server, error)` constructor that:
  - Builds the inner `router.Server` via the existing `router.NewServer`
  - Creates `shutdownCtx` / `shutdownCancel`
  - Returns a ready-to-serve `*Server`
- [ ] HTTP mux foundation: pick the routing primitive (`http.ServeMux` from stdlib is preferred — `gin` stays only in [proxy/](../proxy/) and is removed in Phase 10). Wire a single `*http.ServeMux` field on `Server` that subsequent phases register handlers against.
- [ ] `ServeHTTP(w, r)` delegates to the mux. Register the actual model-dispatched routes against `router.Server.ServeHTTP` (no catch-all) so unknown paths return 404 instead of being silently routed:
  - OpenAI-compatible POST: `/v1/chat/completions`, `/v1/responses`, `/v1/completions`, `/v1/messages`, `/v1/messages/count_tokens`, `/v1/embeddings`, `/reranking`, `/rerank`, `/v1/rerank`, `/v1/reranking`, `/infill`, `/completion`
  - Versionless aliases under `/v/...` (issue #728) — strip the `/v` prefix before forwarding
  - Audio: `POST /v1/audio/speech`, `POST /v1/audio/voices`, `GET /v1/audio/voices`, `POST /v1/audio/transcriptions`
  - Image: `POST /v1/images/generations`, `POST /v1/images/edits`
  - sd.cpp: `POST /sdapi/v1/txt2img`, `POST /sdapi/v1/img2img`, `GET /sdapi/v1/loras`
- [ ] `Shutdown(timeout time.Duration) error` that cancels `shutdownCtx` and delegates to `router.Server.Shutdown` — keep the inflight-drain ordering documented in the cross-cutting concerns section.
- [ ] `SetVersion(buildDate, commit, version string)` setter mirroring [proxymanager.go:1220](../proxy/proxymanager.go#L1220) so [cmd/newrouter/main.go](../cmd/newrouter/main.go) can stamp build info before `ListenAndServe`.
- [ ] Hot-reload story: confirm `server.Server` can be constructed and shut down repeatedly so the existing reload path in [main.go:126](../cmd/newrouter/main.go#L126) keeps working when it swaps the active server.
- [ ] Update [cmd/newrouter/main.go](../cmd/newrouter/main.go) to construct `server.New(cfg, proxyLog, upstreamLog)` instead of `router.NewServer(...)`.
- [ ] Tests in `internal/server/server_test.go`:
  - Construction succeeds for matrix and group configs
  - `ServeHTTP` round-trips to the inner router for a known model
  - `Shutdown` returns within timeout and is idempotent

---

## Phase 5 — Custom HTTP endpoints

Goal: add the non-model-dispatched endpoints. `router.Server` (Phase 1/2) already handles all model-routed traffic via the explicit routes registered in Phase 4. This phase only adds endpoints that need bespoke handlers outside that dispatch.

- [ ] `GET /v1/models` listing — local models + peer models, with aliases and metadata (see [proxymanager.go:588](../proxy/proxymanager.go#L588))
- [ ] `GET /health` and `GET /wol-health`
- [ ] `GET /favicon.ico`
- [ ] `GET /` → redirect to `/ui`

### Phase 5a — Request-body filters

These run on JSON requests before `router.Server` forwards them upstream — they belong as a middleware (use `internal/chain` from Phase 3) wrapping the model-dispatched routes registered in Phase 4, not as new endpoints. Config comes from `ModelConfig.Filters`:

- [ ] `UseModelName` rewrite (issue #69)
- [ ] `StripParams` removal (issue #174)
- [ ] `SetParams` injection (issue #453)
- [ ] `SetParamsByID` per-alias overrides

### Phase 5b — Auth & CORS

Cross-cutting middleware (use `internal/chain` from Phase 3) applied at the `server.Server` mux level so it covers both the custom endpoints above and the router-dispatched traffic:

- [ ] API key middleware accepting `Authorization: Bearer`, `Authorization: Basic`, and `x-api-key`; strip these headers before upstream
- [ ] OPTIONS preflight handler with `SanitizeAccessControlRequestHeaderValues` (see [proxy/sanitize_cors.go](../proxy/sanitize_cors.go))
- [ ] `Access-Control-Allow-Origin` echo on `/v1/models` when `Origin` is set

---

## Phase 6 — Upstream passthrough

- [ ] `GET /upstream` → redirect to `/ui/models`
- [ ] `ANY /upstream/<model>/<path>` proxy with multi-segment model name resolution (see `findModelInPath` at [proxymanager.go:673](../proxy/proxymanager.go#L673))
- [ ] Canonical-form redirect: `/upstream/model` → `/upstream/model/` using 301 (GET/HEAD) or 308 (other methods) to preserve method
- [ ] Path rewrite before forwarding (strip `/upstream/<model>` prefix)

---

## Phase 7 — Operations endpoints

- [ ] `GET /unload` — stop all processes (`StopImmediately`)
- [ ] `GET /running` — JSON list of ready processes with `model`, `state`, `cmd`, `proxy`, `ttl`, `name`, `description`. Matrix and group implementations differ — see [proxymanager.go:1167](../proxy/proxymanager.go#L1167)
- [ ] `GET /logs` and `GET /logs/stream[/:logMonitorID]` — historical + live tail of proxy/upstream logs (handlers in [proxy/proxymanager_loghandlers.go](../proxy/proxymanager_loghandlers.go))
- [ ] Startup preload hook (`Hooks.OnStartup.Preload`) — fires `GET /` against each model in the background; emits `ModelPreloadedEvent`

---

## Phase 8 — Metrics, perf, and SSE

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

## Phase 9 — UI serving

- [ ] Embed the React/Svelte build (see `proxy/ui_embed.go` / `proxy/ui_compress.go`)
- [ ] `GET /ui/*filepath` with brotli/gzip-aware `ServeCompressedFile`
- [ ] SPA fallback to `index.html` for non-file paths under `/ui`

---

## Phase 10 — Cutover

- [ ] Swap `llama-swap.go` to delegate to `cmd/newrouter` (or rename newrouter to be the primary entrypoint)
- [ ] Update `Makefile` build targets
- [ ] Update docs / README references to the legacy binary
- [ ] Remove `proxy/proxymanager*.go` and `gin-gonic` dependency once nothing imports them
- [ ] Run `make test-all` and confirm concurrency suite still passes against the new entrypoint

---

## Cross-cutting concerns to keep in mind

- **Single body read**: legacy and newrouter both buffer the request body once. When adding filters (Phase 5a), make sure the buffered bytes flow through `Content-Length` / `transfer-encoding` cleanup as in [proxymanager.go:872](../proxy/proxymanager.go#L872).
- **Streaming flag in context**: legacy stashes `streaming` and `model` under `proxyCtxKey`. The new router uses `ModelKey` / `ModelIDKey` — pick one set of keys and use them consistently for metrics + log handlers.
- **Matrix vs Group divergence**: any handler that calls `swapProcessGroup` or `findGroupByModelName` in the legacy needs a matrix branch too. The new router's `Router` interface already abstracts this — preserve that abstraction rather than reintroducing the branch in every handler.
- **Shutdown ordering**: `httpServer.Shutdown` must drain inflight requests _before_ `Server.Shutdown` tears down processes, otherwise inflight requests 502. Current newrouter ordering at [main.go:87](../cmd/newrouter/main.go#L87) is correct — keep it.
