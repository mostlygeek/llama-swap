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

Goal: move shared infrastructure packages out from under `proxy/` so the new router does not depend on the legacy proxy tree. This is a prerequisite for retiring `proxy/` in Phase 8.

---

## Phase 2 — Server lifecycle parity -- Completed.

Goal: make `cmd/newrouter` a drop-in replacement for the legacy binary's process model, _without_ yet adding any extra HTTP endpoints.

---

## Phase 3 — `internal/chain` package -- Completed.

API: `chain.New(mws...).Then(final)` for ServeMux registration; `Append` returns an extended Chain without mutating the receiver, so a base stack (auth/CORS) can be reused across many routes with per-route additions.

---

## Phase 4 — `internal/server` package scaffolding (ProxyManager replacement) -- Completed.

Goal: build the [internal/server](../internal/server/) package so it can stand in for [proxy.ProxyManager](../proxy/proxymanager.go#L67) — the mux, lifecycle, model dispatch, custom endpoints, request filters, auth/CORS, and upstream passthrough. After this phase, `cmd/newrouter/main.go` constructs a `server.Server` instead of a bare `router.Server`.

The legacy `ProxyManager` collapses three concerns into one struct: the HTTP mux, the model→process router, and the cross-cutting services (loggers, metrics, perf, inflight counter, version). The new layout keeps the `router.Router` implementations focused on model dispatch and lets `internal/server.Server` own the mux and all cross-cutting middleware. `server.Server` builds the `local` and `peer` routers directly and dispatches between them itself, so it fully **supersedes `internal/router.Server`** — see the cleanup item below.

The phase is split into sub-phases that can land and be tested independently:

| Sub-phase | Scope                                                                      |
| --------- | -------------------------------------------------------------------------- |
| 4a        | package scaffolding — struct, `New`, `ServeHTTP`, `Shutdown`, model routes |
| 4b        | custom (non-model-dispatched) HTTP endpoints                               |
| 4c        | request-body filter middleware                                             |
| 4d        | auth & CORS middleware                                                     |
| 4e        | upstream passthrough                                                       |

The package is split by concern across stub files already in place:

| File         | Responsibility                                  | Filled in by           |
| ------------ | ----------------------------------------------- | ---------------------- |
| `server.go`  | `Server` struct, `New`, `ServeHTTP`, `Shutdown` | 4a                     |
| `log.go`     | `muxlog` combined logger; `/logs` handlers      | 4a                     |
| `auth.go`    | `CreateAuthMiddleware`                          | 4d                     |
| `filters.go` | request-body filter middleware                  | 4c                     |
| `api.go`     | llama-swap-specific API handlers                | 4b / Phase 5 / Phase 6 |
| `ui.go`      | embedded UI serving                             | Phase 7                |

### Phase 4a — package scaffolding -- Completed.

`server.Server` owns the mux, the `local`/`peer` routers, `muxlog`, and a
shutdown context. `New` builds the routers, registers all model-dispatched
routes on a stdlib `http.ServeMux`, and wraps the mux with the global CORS
middleware. `localPeerHandler` resolves the model once via `router.FetchModel`
and dispatches to `local` or `peer`. `Shutdown` stops both routers in parallel
and is idempotent. `cmd/newrouter/main.go` now constructs `server.New(...)`;
`internal/router/server.go` and `server_test.go` were removed as dead code.

### Phase 4b — Custom HTTP endpoints -- Completed.

`GET /v1/models` (local + peer models, aliases, metadata), `GET /health`,
`GET /wol-health`, and `GET /` → `/ui` are registered. `GET /favicon.ico` is
deferred to Phase 7 since it requires the embedded UI filesystem.

### Phase 4c — Request-body filters -- Completed.

`CreateFilterMiddleware` (in `filters.go`) applies `UseModelName`,
`StripParams`, `SetParams`, and `SetParamsByID` to JSON requests, then
re-attaches the body with `Content-Length` / `Transfer-Encoding` cleanup.

### Phase 4d — Auth & CORS -- Completed.

`CreateAuthMiddleware` validates API keys (Bearer / Basic / `x-api-key`) and
strips the headers before upstream. `CreateCORSMiddleware` answers OPTIONS
preflight; `/v1/models` echoes the `Origin`.

### Phase 4e — Upstream passthrough -- Completed.

`GET /upstream` → `/ui/models`, and `/upstream/<model>/<path>` proxies to the
resolved model with multi-segment name resolution, canonical-form redirect
(301/308), and prefix stripping.

---

## Phase 5 — Operations endpoints -- Completed.

A new `router.LocalRouter` interface embeds `Router` and adds `RunningModels()`
and `Unload(timeout, models...)`, both implemented once on `baseRouter` so
`Group` and `Matrix` share them — the legacy matrix/group divergence at
[proxymanager.go:1167](../proxy/proxymanager.go#L1167) collapses since
`baseRouter` already unifies process storage. `Peer` does not implement it;
`Server.local` is typed `LocalRouter`, `Server.peer` stays `Router`.

`GET /unload` stops every local process; `GET /running` lists non-stopped
processes joined against config for `cmd`/`proxy`/`ttl`/`name`/`description`.
`startPreload` fires a background `GET /` at each `Hooks.OnStartup.Preload`
model and emits `shared.ModelPreloadedEvent`.

---

## Phase 6 — Metrics, perf, and SSE -- Completed.

`perf.Monitor` is created and started in `cmd/newrouter/main.go` (it outlives
config reloads via `UpdateConfig`) and passed into `server.New`. `GET /metrics`
serves `perf.Monitor.MetricsHandler()` output, 503 when disabled.

`internal/process` emits `shared.ProcessStateChangeEvent` from `setState`.
`server.inflightCounter` (atomic) + `CreateInflightMiddleware` track
model-dispatched requests and emit `InFlightRequestsEvent`. `metricsMonitor`
(in `metrics.go`) parses token usage from upstream responses via
`CreateMetricsMiddleware`.

The `/api` group (API-key protected) is registered: `POST /api/models/unload`,
`POST /api/models/unload/{model...}`, `GET /api/events` (SSE: `modelStatus` /
`logData` / `metrics` / `inflight`), `GET /api/metrics`, `GET /api/performance`
(`?after=` RFC3339 filter), `GET /api/version`. `GET /api/captures/{id}`
returns 501 until 6f.

### Phase 6f — Request/response captures -- Completed.

`proxy/cache` moved to `internal/cache`. `metricsMonitor` stores zstd+CBOR
`ReqRespCapture` records in a sized `cache.Cache` (`captureBuffer` MB, 0
disables). `CreateMetricsMiddleware` buffers request body/headers before
dispatch; `record` builds the capture per a `captureFieldsByPath` table
(`captures.go`) that trims large audio/image payloads, defaulting JSON routes
to `captureAll`. `GET /api/captures/{id}` decompresses and returns the capture;
`getMetrics` resolves `HasCapture` against the cache.

---

## Phase 7 — UI serving -- Completed.

`internal/server/ui.go` embeds `ui_dist` and serves it. `GET /ui/` is
brotli/gzip-aware via `serveCompressedFile`; unknown paths without a file
extension fall back to `index.html` for SPA routing. `GET /favicon.ico` serves
from the same embedded FS. The Makefile `ui` target copies the vite build into
`internal/server/ui_dist`; a committed `placeholder.txt` keeps the embed valid
before a build runs.

---

## Phase 8a - Review Part I

- [x] All functionality from the proxy package has been migrated in the above phases — with the remaining gaps listed in Phase 8b
- [x] Test coverage at or exceeds the level from the proxy package — `internal/server` now at 76.6% vs 73.9% (`proxy`)

### Findings

**Gap 1 — Request logging middleware missing -- Resolved.**

`CreateRequestLogMiddleware` ([log.go](../internal/server/log.go)) records one
access-log line per request to `s.proxylog` in the legacy format
`clientIP "METHOD PATH PROTO" status bodySize "UA" duration`, skipping
`/wol-health`, `/api/performance`, and `/metrics`. A `statusRecorder` captures
the status/body size (forwarding `Flush` for SSE) and `clientIP` honours
`X-Forwarded-For` / `X-Real-IP`. It is wired as the outermost middleware in
`routes()`, wrapping the CORS layer.

**Gap 2 — Per-model log streaming not supported -- Resolved **

`Server.getLogger` ([log.go:50](../internal/server/log.go#L50)) only handles `""`, `"proxy"`, and `"upstream"`. The legacy `ProxyManager.getLogger` ([proxymanager_loghandlers.go:92](../proxy/proxymanager_loghandlers.go#L92)) additionally resolves a model ID against the active process groups / matrix and returns that process's logger. Callers of `GET /logs/stream/<modelID>` will get a 400 instead of the model's live log stream.

**Gap 3 — `UseModelName` not applied to multipart form endpoints -- Resolved.**

`CreateFormFilterMiddleware` ([filters.go](../internal/server/filters.go)) parses
`multipart/form-data` requests, rewrites the `model` field with `UseModelName`,
reconstructs the body via `rewriteMultipartModel`, and re-attaches it with
`Content-Type` / `Content-Length` cleanup. It runs in `modelChain` after the
JSON `filterMW`; each is a no-op for the other's Content-Type. Audio
transcription (`/v1/audio/transcriptions`) and image edit (`/v1/images/edits`)
now honour `use_model_name`.

**Coverage gaps (0 % functions) -- Resolved.**

The functions previously at 0 % (`handleListModels`, `handleMetrics`,
`handleRootRedirect`, `handleUpstreamRedirect`, `handleUpstream`,
`findModelInPath`, `handleAPICapture`, `handleAPIUnloadAll`,
`handleAPIUnloadModel`, `CreateAuthMiddleware`, `extractAPIKey`,
`handleLogStream`, `applyFilters`, `decompressBody`, `filterAcceptEncoding`,
`handleUI`, `handleFavicon`) now have tests across `auth_test.go`, `api_test.go`,
`filters_test.go`, `log_test.go`, and `extras_test.go`.

---

### Phase 8b - Fill gaps discovered in Phase 8a

- [x] **Add request-log middleware** — `CreateRequestLogMiddleware` ([log.go](../internal/server/log.go)) records `clientIP "METHOD PATH PROTO" status bodySize "UA" duration` to `s.proxylog`, skips `/wol-health` / `/api/performance` / `/metrics`, and is wired as the outermost middleware in `routes()`.
- [x] **Extend `getLogger` with model-ID resolution** — add a `default:` branch to `Server.getLogger` ([log.go:50](../internal/server/log.go#L50)) that resolves the ID via `s.local` (using a new `LocalRouter.GetProcess(name)` method or equivalent) and returns that process's `Logger()`. Match the fallback behaviour: return a 400 with `"invalid logger. Use 'proxy', 'upstream' or a model's ID"` when not found.
- [x] **`UseModelName` rewrite for multipart endpoints** — `CreateFormFilterMiddleware` parses `multipart/form-data`, rewrites the `model` field according to `UseModelName`, reconstructs the body, and updates `Content-Type` / `Content-Length`. It is wired into `modelChain` after the JSON filter.
- [x] **Raise test coverage to ≥ 74 %** — `internal/server` now at 76.1%; tests added for every 0 % function across `auth_test.go`, `api_test.go`, `filters_test.go`, `log_test.go`, and `extras_test.go`.

---

## Phase 8c - Review Part II (entrypoint comparison)

A second pass comparing [cmd/newrouter/main.go](../cmd/newrouter/main.go) against
the legacy [llama-swap.go](../llama-swap.go) + [proxy.New](../proxy/proxymanager.go#L104)
surfaced four more gaps, all in logger setup.

**Gap 4 — `LogToStdout` config ignored -- Resolved.**

`cmd/newrouter/main.go` previously hardcoded `proxyLog` / `upstreamLog` to
`os.Stdout`, and the old `muxlog()` helper built a Monitor that nothing wrote
into — so `logToStdout` had no effect and `/logs` (combined history) was always
empty. `server.NewLoggers` ([log.go](../internal/server/log.go)) now replicates
the legacy switch: `proxy` / `upstream` monitors feed `muxLog` (or `io.Discard`)
per `none` / `both` / `upstream` / `proxy`, so `muxLog` accumulates the combined
history. `server.New` takes `muxlog` as a parameter. The loggers outlive config
reloads, so a `LogToStdout` change requires a restart to take effect.

**Gap 5 — `LogTimeFormat` config ignored -- Resolved.**

`cmd/newrouter/main.go` now maps `cfg.LogTimeFormat` to a Go time layout via the
`logTimeFormats` table and applies it (alongside log level) to the proxy and
upstream monitors in `applyLogSettings`, re-applied on config reload.

**Gap 6 — `LogRequests` deprecation warning missing.**

The legacy [proxymanager.go:127](../proxy/proxymanager.go#L127) warns when the
deprecated `logRequests` config key is set. `cmd/newrouter` does not. Low
priority — left open.

**Gap 7 — PID debug log missing -- Resolved.**

`cmd/newrouter/main.go` now logs `PID: %d` at debug level after `applyLogSettings`,
matching [llama-swap.go:71](../llama-swap.go#L71).

---

## Phase X (tbd) — Cutover

- [ ] Swap `llama-swap.go` to delegate to `cmd/newrouter` (or rename newrouter to be the primary entrypoint)
- [ ] Update `Makefile` build targets
- [ ] Update docs / README references to the legacy binary
- [ ] Remove `proxy/proxymanager*.go` and `gin-gonic` dependency once nothing imports them
- [ ] Run `make test-all` and confirm concurrency suite still passes against the new entrypoint

---

## Cross-cutting concerns to keep in mind

- **Single body read**: legacy and newrouter both buffer the request body once. When adding filters (Phase 4c), make sure the buffered bytes flow through `Content-Length` / `transfer-encoding` cleanup as in [proxymanager.go:872](../proxy/proxymanager.go#L872).
- **Streaming flag in context**: legacy stashes `streaming` and `model` under `proxyCtxKey`. The new router uses `ModelKey` / `ModelIDKey` — pick one set of keys and use them consistently for metrics + log handlers.
- **Matrix vs Group divergence**: any handler that calls `swapProcessGroup` or `findGroupByModelName` in the legacy needs a matrix branch too. The new router's `Router` interface already abstracts this — preserve that abstraction rather than reintroducing the branch in every handler.
- **Shutdown ordering**: `httpServer.Shutdown` must drain inflight requests _before_ `Server.Shutdown` tears down processes, otherwise inflight requests 502. Current newrouter ordering at [main.go:87](../cmd/newrouter/main.go#L87) is correct — keep it.
