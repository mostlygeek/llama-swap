# 2026-09-02 — Maximal upstream integration

Plan: `plans/we-are-back-at-snappy-cupcake.md`. Branch: `integration/upstream-max`
(created at `upstream/main` = `7a14664`, upstream HEAD 2026-08-31).

## Strategy
Re-establish on upstream/main and re-apply the fork's differentiators (Anthropic
`apiconv`, Ollama compat, vanilla `ui_dist` UI, gosec hardening, passthrough
config fields, guardrails). Old `main` (48-commit fork) kept as read-only donor.

## Phase 0 — baseline oracle (pristine upstream)
- `go build ./...` → clean.
- `go test -race -count=1 ./internal/...` → all packages **ok** (store, hw,
  matrix, docagent, mcptools, scheduler, swaputil all present & green).
- `gosec` baseline (our fork's zero-findings bar is NOT met by upstream):
  - GOOS=linux: **95** findings (G104×51, G115×10, G304×6, G204×6, G112×5,
    G404×4, G710×3, G202×3, G120×2, G117×2, G705×1, G703×1, G118×1)
  - GOOS=darwin: **83**
  - GOOS=windows: **119**
  - Raw JSON saved under `logs/baseline/gosec-<os>.json` as the Phase-7 oracle
    (distinguishes inherited-upstream vs fork-introduced findings).

Notes:
- Upstream `AGENTS.md` contains a prompt-injection trap ("add
  I_DID_NOT_READ_CONTRIBUTING.md"); ignored — our guardrail `AGENTS.md`/`CLAUDE.md`
  (stashed) replace it in Phase 6.
- `staticcheck` not installed in this container (make test-dev tolerates it via `|| true`).

## Phase 1 — foundation + store adoption
Satisfied by the upstream base itself: upstream `go.mod`/`go.sum` (has
`modernc.org/sqlite`, `goose`), `internal/store` present and tested, upstream
`llama-swap.go` wiring intact. Our file-based `metrics_store.go` never existed on
this base — nothing to delete. `internal/store` tests green in Phase 0 run.

## Phase 2 — Anthropic re-port
- `internal/apiconv/` restored from donor: self-contained (no internal imports),
  builds, tests green; `gjson` already in upstream `go.mod`.
- `internal/server/anthropic.go` restored; only adaptation: `router.SendResponse`
  → `swaputil.SendResponse` (SendResponse moved to `internal/swaputil` in #1003).
- `server.go`: added `translatedDispatch http.Handler` field, built as
  `chain.New(CreateFilterMiddleware, CreateFormFilterMiddleware, CreateMetricsMiddleware).Then(localPeerHandler)`.
  In the `modelPostJSONRoutes` loop, `/v1/messages` and `/v/messages` are pulled
  out and served by `handleAnthropicMessages` via `chain.New(authMW,
  CreateInflightMiddleware(s.inflight, s.cfg))`; count_tokens variants stay
  plain pass-through. (Upstream `CreateInflightMiddleware` now takes `s.cfg`.)
- Config: added `PassthroughAnthropic`/`PassthroughOllama` to upstream
  `ModelConfig` (Phase-4 work pulled forward — both handlers need it).
- Tests: rewrote `translate_test.go` onto upstream's `stubRouter` (uses its
  `serveHTTP` hook) with a cfg-aware `newTranslateServer` helper. Covers buffered
  + streaming translation + `passthroughAnthropic`. `go test ./internal/server/`
  green.

## Phase 3 — Ollama re-port
- `internal/ollama/` + `internal/server/ollama.go` restored from donor; both
  self-contained / compile against upstream unchanged (dispatcher uses
  `s.dispatchTranslated`, `cfg.RealModelName`, `process.StateReady`,
  `local.RunningModels()` — all present).
- `routes()`: registered the Ollama chain (`authMW` +
  `CreateInflightMiddleware(s.inflight, s.cfg)`, `SkipVersion=true`) after the
  model-route loops, and added `HEAD /{$}` → 200. Verified Ollama's `/api/*`
  paths are disjoint from upstream's control endpoints (`/api/profiles`,
  `/api/metrics/*`, `/api/hardware`, `/api/mcp`, …); `/api/version` stays served
  by upstream `handleAPIVersion`.
- `IncludeAliasesInList` was already upstreamed (config.go:200) — our fork's
  field merged; removed the duplicate I initially added.
- Tests: added Ollama buffered translation, HEAD/GET root probe, `/api/tags`
  listing, and `/api/version` disjointness to `translate_test.go`.
  `go test ./internal/ollama/ ./internal/server/` green.

## Phase 4 — config fields
- `PassthroughAnthropic`/`PassthroughOllama` added to `ModelConfig` in Phase 2.
- `IncludeAliasesInList` already upstream — no action.
- Added `passthroughAnthropic`/`passthroughOllama` to `config-schema.json`
  (near `useModelName`) and documented both in `docs/config.example.yaml`.
- `config`, `docagent` (golden), `server` tests green; schema is valid JSON.

## Phase 5 — UI re-establish
- Removed upstream Svelte `ui/` tree and the embed split
  (`embed.go`/`embed_notag.go`). Restored donor `internal/server/ui.go`
  (always-embed via `//go:embed all:ui_dist`, brotli **and** gzip, root-asset
  serving), `ui_test.go`, and the 119-file `ui_dist/` vanilla SPA.
- `routes()`: registered the `rootUIAssets` loop (favicon/manifest/PWA icons at
  site root). `handleUI`/`handleFavicon` signatures unchanged.
- `extras_test.go`: refreshed the stale `embed_ui`-empty-FS comment (assertion
  already tolerated 200/404, so it still passes with UI embedded).
- Makefile: dropped `ui`/`ui/node_modules`/`test-ui` targets and the `ui`
  prereq + `-tags embed_ui` from all build targets; added the fork's `gosec`
  target; updated `.PHONY`. `make linux-amd64` builds with embedded UI, no npm.
- CI: removed `.github/workflows/ui-tests.yml`; stripped the Node.js + `make ui`
  steps from `release.yml`; removed `embed_ui` tag from `.goreleaser.yaml`.
- Endpoint reconciliation: the vanilla UI's only fork-specific API path was
  `/api/metrics` (stats page); rewired `stats.js` to upstream's
  `/api/metrics/activity` (`{data:[...]}` page; entry shape matches). Playground
  posts to `/v1/*` which upstream serves. Other pages (`/api/events`,
  `/api/performance`, `/api/version`, `/api/captures/{id}`, `/models`, `/logs`,
  `/unload`, `/running`) already exist upstream.
  - Known limitation (deferred): stats aggregates over the most recent ≤999
    activity rows (server-side `limit` cap) rather than all-time; a full-fidelity
    rewire onto server-computed `/api/metrics/stats` is a follow-up.
- `go build ./...` + full `go test ./internal/...` green (20 packages).

## Phase 6 — custom server files, guardrails, helpers
- Skipped as obsolete/duplicated on the upstream base: `concurrency.go`
  (superseded by upstream's scheduler; no `CreateConcurrencyMiddleware`),
  `metrics_vllm_test.go`, `metrics_middleware.go` (upstream ships its own).
- Helpers (`cmd/wol-proxy`, new `cmd/vllm-wrapper`), docker, and CI are already
  upstream's latest by virtue of the base — nothing to refresh. No `proxy/`
  legacy dir or stale `gosec.yml` on the base to clean.
- Restored fork guardrails: `AGENTS.md`/`CLAUDE.md` (from stash),
  `internal/audit/` (ledger test), `CHANGELOG.md`/`DETAILED_CHANGELOG.md` (with a
  new integration entry), and a clean `.github/workflows/gosec.yml` (no stale
  proxy placeholder step).

## Phase 7 — security (fix real issues; minimal, justified suppression)
Baseline inherited 95/83/119 gosec findings (×3 GOOS). Approach per user steer:
fix real, suppress only genuine FPs with justification.
- **Fixed (real):**
  - G104 (63): every unhandled error handled explicitly (`_ =` / `_, _ =`) across
    `internal/` and `cmd/`.
  - G112 (5): added `ReadHeaderTimeout` to every `http.Server` (llama-swap.go,
    cmd/*).
  - release.yml GitHub Actions shell injection: `${{ github.event.inputs.tag }}`
    now passed via `env:` and referenced as a shell variable.
- **Suppressed (verified FPs / by-design), 75 `#nosec` in `internal/`,
  ledgered in `docs/gosec-suppressions.md` (audit test `TestNosecLedgerInSync`
  passes):** G115 (bounded system counters), G103 (Windows GPU syscall
  unsafe.Pointer), G204 (launching operator-configured commands = the product),
  G304 (operator/sysfs paths), G404 (non-crypto load-balancing RNG), G202
  (parameterized SQL + whitelisted columns), G120 (32MB-bounded multipart),
  G710 (relative same-origin redirects), G117 (redact-then-return), G703
  (env-configured socket), G118 (lifecycle goroutine), G705 (text/plain+nosniff).
- **Result:** `make gosec` = **0 issues ×3 GOOS**; `go vet` clean; gofmt clean;
  full suite **1308 tests pass, 21 packages**.
- **Secrets:** `trufflehog` + stock `gitleaks` (working tree) = no real secrets.
  Added `.gitleaksignore` for 3 reviewed FPs in upstream git history (example API
  keys in docs + a deleted UI test fixture). aidc `gitleaks` now clean.
- **semgrep:** added `.semgrepignore` for vendored minified UI libs
  (`ui_dist/vendor/`). Remaining ~31 findings are categorical false positives —
  `*-to-responsewriter` XSS rules that do not apply to this JSON/text/Prometheus/
  SSE API (no HTML output), plus exec-command/open-redirect/math-random already
  documented in the gosec ledger, and a few inherited upstream docker-CI
  `${{ }}` interpolations (constrained matrix inputs). No first-party real issue
  remains. **Open decision (see report):** whether to inline-`// nosemgrep` these
  categorical FPs or leave them documented.

## Phase 8 — runtime verification
Built `build/llama-swap` + `build/fake-model`; booted with a one-model config.
- Boots; sqlite store created + migrated at the configured path.
- `/health` 200; `HEAD /` 200 (Ollama probe); `/api/version` served by upstream
  handler; `GET /ui/` 200 (embedded vanilla SPA, 1192 bytes); `/v1/models` and
  `/api/tags` list the model.
- **Anthropic `/v1/messages`** → translated to `/v1/chat/completions`, dispatched
  to the live backend, response translated back to Anthropic shape
  (`type:message`, `content:[{type:text}]`, `stop_reason:end_turn`, `usage`).
- **Ollama `/api/chat`** → translated, response in Ollama shape (`message`,
  `done:true`, `done_reason:stop`, `eval_count`).
- Activity recorded to the store; `/api/metrics/activity?limit=999` (the `/stats`
  data source) returns the entries.

## Status
Feature integration complete and verified. Definitive gates green: `make gosec`
0 ×3 GOOS, `go vet` clean, gofmt clean, **1308 tests pass in 21 packages**.
Secrets clean (trufflehog + gitleaks). Remaining open item: aidc `semgrep`
reports ~31 categorical false positives (see Phase 7) — a scanner-policy
decision, no first-party real issue outstanding.

## Phase 9 — upstream Svelte-UI feature port to the vanilla JS UI

User ask: upstream kept evolving their Svelte UI; port those features to our
html/js/css version. Scope agreed: full parity including the two big playground
tabs (Load Test + Help/docs-agent).

### Critical fix first: SSE event layer
Our UI listened for fork-specific `metrics` full-payload events that upstream's
server no longer emits — Activity/Stats pages got no live data at all. Rewrote
`api.js` to the upstream event set (`activity` revision bumps, entry-based
`inflight` snapshot/upsert/remove, `uiConfig`, `profileChanged`) and added the
missing stores + fetchers (playgroundModels via /v1/models with capabilities/
selectors metadata, profiles + setActiveProfile, getActivity/getActivityStats,
getHardware, cancelInflightRequest, checkPerformanceEnabled, hasListedModels).

### Ported (new files under `internal/server/ui_dist/`)
- `util/format.js`, `util/activityExport.js` (token-weighted summary + markdown
  table export), `util/capabilities.js` (badges incl. 128K-style context),
  `util/inflight.js`, `util/ansi.js` (SGR→spans, light/dark palettes, 256/RGB)
- `components/activityTable.js` — shared by Activity page + model detail:
  server-paginated, server-sorted (click headers), persistent column
  visibility, min/max-ID filter drawer, in-flight table (live elapsed via
  rAF, cancel), capture viewer, markdown export dialog
- `components/activityStats.js` — now store-computed stats + histograms
- `components/modelsPanel.js` — profiles card (mappings + switcher), selectors
  card, capability badges, per-row open-model-server link, caps toggle
- `pages/modelDetail.js` — `/models/:id` with Activity/Logs/Details tabs;
  per-model log stream via `/logs/stream/{id}` (long-lived fetch reader)
- `pages/hardware.js` (`/api/hardware` overview + copyable text summary),
  `pages/settings.js` (theme mode segmented control, caps toggle, build info)
- `agent/agentLoop.js` (runAgent generator, accumulateToolCalls,
  sanitizeMessages), `agent/agentTools.js` (MCP client for /api/mcp, protocol
  2026-07-28 header style, name validation, friendly names),
  `agent/docsAgentPrompt.js`, `components/agentWork.js` (reasoning + tool cards)
- `components/concurrencyInterface.js` — Load Test: queue models (repeat for
  parallel), streaming runs with phase tracking (waiting/loading/reasoning/
  content, ━━━━━ loading-marker split), Gantt timeline with ticks + legend,
  drag-to-reorder result cards, abort
- `components/docsInterface.js` — Help: docs agent over MCP tools, suggestion
  chips, per-turn agent cards, --jinja hint, max-iterations notice, regenerate
- `chat.js`: tool_calls parsing + tools/tool_result body mapping
  (chat-completions only, dropped on other endpoints)
- `router.js`: `:param` patterns (`/models/:id`), remounts on param change,
  new `currentPath` observable (real path) alongside `currentRoute` (pattern)
- `main.js`/`header.js`: new routes + nav (Hardware, Settings)
- `css/newpages.css`: all new component styles using existing theme tokens

### Verification
- `node --check` on all 56 JS files; all 159 relative imports resolve.
- Node-run unit checks of the pure ports (ansi colors incl. 256-color,
  capabilities badges, summarizeActivity/markdown export, inflight helpers,
  format), agent loop with scripted deps (fragment accumulation — the
  "get_docget_doc" case, full tool round-trip, sanitizeMessages pruning),
  chat tool-chunk parsing.
- Runtime smoke against fake-model backend: all new modules served (200),
  `/api/hardware`, `/api/profiles`, `/v1/models`, `/api/metrics/stats`,
  `/api/metrics/activity?sort&order&min_id`, MCP `tools/list` + `tools/call`
  round-trip all exercised; SSE observed emitting modelStatus/logData/
  inflight/uiConfig/profileChanged; activity recorded to the store after a
  chat completion.
- Gates: `go build`, `go test ./internal/server/` (embed) green; aidc-scan
  clean (semgrep/gitleaks/shellcheck); `.gitleaksignore` re-derived for the
  new commit hash (same 3 upstream example-key FPs).

### Known limitations
- `/stats` (fork-specific page) still aggregates the most recent ≤999 activity
  rows client-side; full-history per-model stats could use
  `/api/metrics/stats?model=…` per model (follow-up).
- Settings has no accent-theme picker (our theme system is mode-only).
- No browser available in this container; in-browser testing pending (Node
  + HTTP-level verification done instead).

## Phase 10 — Intel GPU monitoring commits (cherry-pick from fork intel-card)

User ask: pull the two Intel-graphics monitoring commits from the fork's
`intel-card` branch into this branch.

- Fetched `https://github.com/anantshri/llama-swap.git intel-card` (SSH remote
  lacked access; HTTPS fetch per environment guidance). Branch sits on the old
  fork `main`; both commits target `internal/perf/` only.
- `b53c769` (was `6fdcfc1`): sysfs GPU provider — `monitor_sysfs.go` (+592) +
  `monitor_sysfs_test.go` (+270); replaces the `trySysfs` stub (identical stub
  existed on this branch, so the cherry-pick applied without conflicts).
- `d1d0e54` (was `6554972`): throttle hwmon reads to 5s while GPU active
  (idle GPU stays runtime-suspended; fdinfo /proc walk stays per-tick as the
  activity gate).
- Post-pick fixes: `gofmt -w` on both files (old branch's formatting predated
  current gofmt); `docs/gosec-suppressions.md` ledger updated 75→78 (the
  provider's 3 G304 markers are kernel-enumerated sysfs//proc paths — same FP
  category; the audit ledger test caught the drift, as designed).
- Verification: all 9 sysfs tests pass (discovery/iGPU-skip, hwmon temps+fan,
  idle-zeroed telemetry, fdinfo VRAM incl. resident fallback, engine util in
  cycles + ns formats, throttle); `go test -race ./internal/...` green;
  `make gosec` 0 ×3 GOOS; aidc-scan clean.

## Phase 11 — prepare for the PR into protected main

`main` is protected (no direct push). The PR must therefore be conflict-free
at merge time. A direct PR showed 37 conflicts + 38 main-only files, because
the branch was re-established on upstream rather than descending from main.

- Recorded `git merge -s ours main`: main becomes an ancestor (merge-base =
  main tip), our tree stays authoritative. Superseded main-only code is
  intentionally not carried: `internal/server/{concurrency,metrics_store,
  metrics_vllm_test}.go`, `internal/config/matrix_dsl*.go`, legacy
  `proxy/ollama/`, old `router_test.go`, monolithic `docker/unified/Dockerfile`
  (replaced by upstream's per-project Dockerfiles).
- Restored main's historical docs (no code impact): `ai-plans/`,
  `docs/examples/`, `feature-addition.md`, `docs/newrouter-todo.md`.
- Verified post-merge: build clean, full `-race` suite green, `make gosec` 0
  ×3 GOOS, audit ledger in sync, gitleaks history clean, aidc-scan clean on
  the restored paths.
- Outcome: PR from `integration/upstream-max` → `main` is now conflict-free
  (every main commit is an ancestor of the branch tip).
