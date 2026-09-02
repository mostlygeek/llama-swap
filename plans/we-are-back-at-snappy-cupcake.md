# Plan: Maximal Upstream Integration for the llama-swap Fork

## Context

Our fork (`anantshri/llama-swap`) has diverged heavily from upstream
(`mostlygeek/llama-swap`). As of the merge-base `46cea36` (2026-06-06):

- **Our `main`** is **48 commits ahead** — it added an Anthropic translation
  layer, an Ollama `/api/*` compatibility layer, file-based metrics, a fully
  **replaced UI** (deleted the Svelte `ui/`, committed a no-build vanilla-JS
  SPA under `internal/server/ui_dist/`), gosec hardening, and project
  guardrails (aidc security scanning, changelogs, session logs).
- **Upstream** is **96 commits ahead** — it built a new scheduler (#823/#960),
  selectors (#942), profiles (#935), peer namespaces (#950),
  `internal/matrix`, `internal/hw` hardware detection (#978), ComfyUI (#1002),
  `internal/store` sqlite activity metrics, `docagent`/`mcptools`/reference
  (#1054), a naming refactor (#1003), plus many fixes — **all wired through the
  exact files our fork rewrote** (`internal/server/server.go` `routes()`,
  `internal/config`, `internal/router`).

A straight `git merge` is infeasible: nearly every upstream commit collides
with our rewrites, and the #1003 rename touches symbols our fork references
everywhere. The user wants a **new branch** that pulls in **as much upstream as
feasible (maximal)**, **adopts upstream's sqlite `internal/store`** (dropping
our file-based metrics), refreshes **helper binaries / docker / CI / docs**, and
keeps our fork's identity — all while continuously adding tests and passing
security scans.

**Intended outcome:** an `integration/upstream-max` branch sitting on top of
`upstream/main` with our differentiators re-applied, green on `make test-all`,
`make gosec` (0 findings ×3 GOOS), and `aidc-scan`.

## Strategy: re-establish on upstream, re-apply our differentiators

Instead of cherry-picking 96 entangled commits **into** our tree, branch **from
`upstream/main`** and re-apply only our ~7 differentiators as a curated patch
series. This inherits every upstream feature natively and correctly, and
concentrates the work into a handful of well-scoped hand-merges we fully
control (server `routes()`, `llama-swap.go` wiring, Makefile/CI, `/stats`
rewire). The alternative (cherry-pick-into-ours) was rejected: the #1003 rename
alone makes almost every later commit conflict against our symbol names.

Our old `main` stays untouched as the **donor** — we pull package trees from it
with `git checkout main -- <path>`.

### Differentiators to re-apply
1. **Anthropic**: `internal/apiconv/` (self-contained, tested) + `internal/server/anthropic.go`; routes `POST /v1/messages`, `/v1/messages/count_tokens`, `/v/messages` (+ count variant) intercepted **before** modelChain.
2. **Ollama**: `internal/ollama/` (Dispatcher interface) + `internal/server/ollama.go`; `/api/chat|generate|embed|embeddings|tags|show|ps`, management stubs, `HEAD /` 200 probe.
3. **Vanilla UI**: delete upstream `ui/`, restore `internal/server/ui_dist/` (119 files) + single always-embed `ui.go`; strip npm from Makefile/CI.
4. **gosec hardening**: `#nosec` annotations **re-derived** on the new base + `docs/gosec-suppressions.md` ledger + `internal/audit/ledger_test.go`.
5. **Config fields**: `PassthroughAnthropic`, `PassthroughOllama`, `IncludeAliasesInList` added onto upstream's (grown) config structs.
6. **Custom server files**: `concurrency.go`, `metrics_vllm_test.go`, `translate_test.go`, and a **merged** `metrics_middleware.go` (upstream now ships its own — merge, don't clobber).
7. **Guardrails**: `AGENTS.md`, `CLAUDE.md`, `logs/`, `scripts/ci/`, `.github/workflows/sbom.yml`, `CHANGELOG.md`/`DETAILED_CHANGELOG.md`.

## Phases

### Phase 0 — Branch + baseline oracle
- `git switch -c integration/upstream-max upstream/main`.
- Run `make test-all`, `make gosec` (GOOS linux/darwin/windows), `aidc-scan` on the **pristine upstream base** and record results. This is the regression oracle: anything red later is ours, not inherited.

### Phase 1 — Foundation (upstream verbatim + store adoption)
- Keep **upstream** `go.mod`/`go.sum` (has `modernc.org/sqlite`, `goose`, etc.). Do **not** re-apply our dependency trims.
- Adopt `internal/store` as-is; **delete** `internal/server/metrics_store.go`.
- Take upstream `llama-swap.go` wiring wholesale (`server.New(... st *store.Store, hardware *hw.HardwareSnapshot, refs *docagent.Docs ...)`).
- Verify: `go build ./...`, `go test ./internal/store/...`.

### Phase 2 — Anthropic re-port (hardest edit)
- `git checkout main -- internal/apiconv/ internal/server/anthropic.go internal/server/translate_test.go`.
- In upstream `routes()`: remove the four Anthropic paths from the `modelPostJSONRoutes` loop, then register them explicitly with `auth + inflight` → our handler (mirrors old `server.go:215-227`). Honor `PassthroughAnthropic` by falling back to upstream's modelChain/localPeer path.
- Wrap the handler with metrics middleware so translated calls still record into `store` (for `/stats` parity).
- Expect compile fixups from #1003 renames (logic unchanged).
- Verify: `go test ./internal/apiconv/... ./internal/server/ -run 'Anthropic|Translate'`.

### Phase 3 — Ollama re-port
- `git checkout main -- internal/ollama/ internal/server/ollama.go`.
- Register the Ollama chain (`auth + inflight`, `SkipVersion=true`) **after** upstream's `/api/*` block. Our paths are disjoint from upstream's new `/api/profiles|metrics|hardware|mcp|...` — verify no shadowing. Let upstream's `handleAPIVersion` serve `/api/version`.
- Reconcile root handlers: keep upstream `GET /{$}`, add `HEAD /{$}` 200 only if absent.
- Verify: `go test ./internal/ollama/...`; manual `curl -I /`.

### Phase 4 — Config fields
- Add the three fork fields onto upstream's config structs (do **not** replace upstream config — it grew selectors/profiles/peers/comfyui fields). Update `config-schema.json` + `config.example.yaml` (additive). Wire `IncludeAliasesInList` into upstream's list-models handler.

### Phase 5 — UI re-establish
- Delete upstream `ui/`; `git checkout main -- internal/server/ui_dist/`.
- Replace upstream's three-file embed split (`embed.go` + `embed_notag.go` + `ui.go`) with our single always-embed `ui.go` (gzip+brotli superset); drop `-tags embed_ui` from Makefile/CI.
- **Rewire** `internal/server/ui_dist/js/pages/stats.js` from the deleted file-store to upstream `/api/metrics/activity` + `/api/metrics/stats`.
- **Keep all upstream backend endpoints** even though we drop the Svelte pages — `/api/profiles`, `/api/hardware`, `/api/performance`, `/api/mcp`, `/api/events`, ComfyUI passthrough have standalone API value. Selectors/profiles become **API-only** (no fork-UI screen) — see Deferred.
- Expect a few `ui_test.go`/`server_test.go` expectations to flip (real FS now serves in test builds) — adjust tests, not behavior.

### Phase 6 — Custom server files, guardrails, helpers
- Land `concurrency.go`, `metrics_vllm_test.go`; **merge** our `metrics_middleware.go` with upstream's same-named file.
- Re-apply `internal/audit/ledger_test.go` and guardrails (`AGENTS.md`, `CLAUDE.md`, `logs/`, `scripts/ci/`, `sbom.yml`, changelogs).
- Refresh helpers/docker/CI from upstream: adopt new `cmd/vllm-wrapper`, refresh `cmd/wol-proxy`, docker per-project build stages, split Docker CI; pull relevant docs (skip UI-svelte docs).
- Remove orphaned legacy `proxy/ollama/` (superseded by `internal/ollama`) and the stale `proxy/ui_dist` reference in `gosec.yml`.

### Phase 7 — gosec re-derivation
- Run `make gosec` (×3 GOOS) on the completed tree → **fresh** finding set (our old `#nosec` markers annotate code that no longer exists — do not copy blindly).
- Triage each finding on current lines; add `// #nosec Gxxx -- reason` only where justified; regenerate `docs/gosec-suppressions.md` so `TestNosecLedgerInSync` passes. The enlarged tree (sqlite, hw, matrix, docagent) is a new G-rule source — budget triage time. Separate inherited-upstream findings (Phase 0 baseline) from fork-introduced ones.

### Phase 8 — Full verification (see below).

## Verification

Cadence: each phase runs `go build ./...` + the touched package's tests before advancing — never leave a package red. First full `make gosec` after Phase 6 drives Phase 7. Final gates in Phase 8.

End-to-end (Phase 8):
1. **Build**: `make` / `make linux-amd64 mac windows` — confirm no npm step, `ui_dist` embeds, binary starts with a real config.
2. **Store**: first start creates/migrates the sqlite db (`internal/store/migrations`), no goose errors.
3. **Anthropic bypass**: `curl /v1/messages` (+ `count_tokens`, `/v/messages`) returns a translated Anthropic-shaped response; then set `passthroughAnthropic: true` and confirm raw forwarding via modelChain.
4. **Ollama**: `curl /api/chat|generate|tags|show`; `curl -I /` → 200; `/api/version` answers.
5. **No-collision proof**: `/api/profiles`, `/api/metrics/stats`, `/api/hardware` still resolve to upstream handlers.
6. **UI + metrics**: `/ui/` loads (brotli+gzip); `/stats` renders store-backed data after driving traffic in 3–4.
7. **Upstream smoke**: exercise a selector, `PUT /api/profiles/active`, ComfyUI passthrough, `/api/mcp`.
8. **Gates**: `make gosec` = 0 (×3 GOOS), `make test-all` green, `aidc-scan` clean, `trufflehog filesystem --no-update .` if any credential-like finding.

Every re-ported/merged file ships with tests covering happy path, edge, and error cases (naming: `TestServer_*`, `TestProcessCommand_*`). Update `CHANGELOG.md` + `DETAILED_CHANGELOG.md` and add a `logs/2026-09-02-upstream-max-integration.md` session log.

## Risks / Deferred

- **Vanilla-UI screens for new features** (selectors/profiles/capabilities): shipped **API-only** this pass; hand-authoring vanilla pages is a separate effort.
- **Metrics-middleware unification**: if merging our vs upstream `metrics_middleware.go` is risky, defer recording of translated calls to the store to a follow-up and note the `/stats` gap.
- **gosec zero on the enlarged tree**: may need a second pass; track exotic third-GOOS findings rather than blocking the branch.
- **#1003 rename fallout in apiconv/ollama**: budget compile-fixups; if the passthrough-to-peer branch is deeply entangled, land translate-local-only first and add peer passthrough later.
- **docagent eval workflow / agentic Playground chat**: adopt the code, defer wiring the model-scoring workflow.
- This is a **multi-session** effort; the branch stays separate from `main` until gates pass and the user reviews.
