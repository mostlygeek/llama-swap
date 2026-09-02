# Changelog

All notable changes to this fork are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Entries track this fork's deltas on top of the upstream base (see the fork
notice in [README.md](README.md); divergence baseline: upstream `7a14664`).
Long-form entries with full context live in
[DETAILED_CHANGELOG.md](DETAILED_CHANGELOG.md).

## [Unreleased]

### Added (selective upstream-PR ports)

- Surface the upstream's own log output in the error when a model process exits
  before becoming ready, instead of the opaque "upstream command exited
  prematurely" (upstream PR #897).
- `POST /models/unload` — llama.cpp-compatible named-model unload used by
  Open WebUI (upstream PR #924).
- `setParamsByMatch` request filter: set parameters when a request field
  matches a configured value, plus map/list macros usable as whole values
  (upstream PR #934).
- Per-model output-token caps (`capabilities.max_output_tokens`) enforced after
  user filters, and dynamic reasoning-effort selection for llama.cpp b8605+
  (`capabilities.reasoning` with per-effort budgets); both surfaced as
  `/v1/models` metadata extensions (upstream PR #915). The capability probe is
  bounded by a 2s timeout so an unresponsive upstream cannot stall startup.
- Matrix `+undefined` reference: a set can include every model not named by
  any other set expression (upstream PR #1026).

### Changed

- Re-established the fork on upstream `7a14664` (2026-08-31), inheriting the new
  scheduler, selectors, profiles, peer namespaces, `internal/matrix`,
  `internal/hw`, ComfyUI compatibility, `internal/store` (sqlite activity
  metrics), and the docagent/mcptools reference subsystem. The fork's
  differentiators were re-applied on top rather than merged commit-by-commit.
- Adopted upstream's sqlite `internal/store` for activity metrics, replacing the
  fork's file-based JSON metrics store.

### Added

- Anthropic Messages API translation (`internal/apiconv`): `/v1/messages` and
  `/v/messages` are translated to OpenAI chat-completions and back (buffered +
  streaming), with a `passthroughAnthropic` per-model opt-out.
- Ollama `/api/*` compatibility layer (`internal/ollama`) with a
  `passthroughOllama` opt-out and a `HEAD /` reachability probe.

### Removed

- The Svelte UI and its npm build step: the hand-authored vanilla-JS SPA under
  `internal/server/ui_dist/` is embedded via `//go:embed` (no build tag).

### Added (upstream UI feature port to the vanilla JS UI)

- Activity page rebuilt on the server-backed store: paginated
  `/api/metrics/activity` with server-side sorting, min/max-ID filters,
  live-refresh throttled on the SSE `activity` revision, store-computed
  `/api/metrics/stats` cards + histograms, in-flight requests table with
  cancel, and markdown export of the visible rows.
- SSE layer updated to the upstream event set (`activity`, entry-based
  `inflight`, `uiConfig`, `profileChanged`) — the old fork-specific
  full-metrics-payload events no longer exist server-side.
- Models page: profiles card (active profile, pin mappings, switcher),
  selectors card (targets/strategy/spillover), capability badges + context
  window per model, model-server open link.
- Model detail page (`/models/:id`): Activity / Logs (per-model stream) /
  Details (capabilities) tabs; param-aware hash router.
- Hardware page from `/api/hardware` with a copyable plain-text summary;
  Settings page (theme mode, capability-tag toggle, build info).
- Logs: ANSI color rendering (SGR → styled spans, theme-aware palettes).
- Playground: Load Test tab (concurrent streaming requests with Gantt-style
  phase timeline, drag-to-reorder result cards) and Help tab — the docs agent
  over the server's MCP tools (`/api/mcp`) with a full agent loop
  (tool-call accumulation, sanitize-on-reload, max-iterations continue).
- perf: sysfs GPU provider (Intel xe/i915 + generic hwmon/fdinfo) with hwmon
  reads throttled to 5s while the GPU is active, so hosts without
  nvidia-smi/rocm-smi/LACT (e.g. Intel Arc) get GPU telemetry and idle cards
  stay runtime-suspended. Cherry-picked from the fork's `intel-card` branch.

### Fixed

- `cmd/vllm-wrapper` now cross-compiles for Windows: the `syscall.Kill` call
  (Unix-only) moved behind a build-tagged `stopProcess` helper
  (`stop_unix.go` / `stop_windows.go`), unblocking `GOOS=windows gosec`.
- Documented the DXGI COM interop `gosec` false positives in
  `internal/hw/dxgi_windows.go` (G115 HRESULT/LUID truncation, G103
  `unsafe.Pointer` vtable calls) with inline `#nosec` markers, so the newer
  CI `gosec` (v2.26.1) reports zero findings on the Windows target.

### Security

- Handled every `gosec` G104 unhandled-error finding explicitly and added
  `ReadHeaderTimeout` to every `http.Server` (G112). `make gosec` reports zero
  findings across `GOOS=linux/darwin/windows`; remaining findings are documented
  false positives in [docs/gosec-suppressions.md](docs/gosec-suppressions.md).
- Fixed a GitHub Actions shell-injection in `release.yml` (untrusted
  `workflow_dispatch` input now passed via `env`).
