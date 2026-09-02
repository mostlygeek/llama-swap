# Changelog

All notable changes to this fork are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Entries track this fork's deltas on top of the upstream base (see the fork
notice in [README.md](README.md); divergence baseline: upstream `7a14664`).
Long-form entries with full context live in
[DETAILED_CHANGELOG.md](DETAILED_CHANGELOG.md).

## [Unreleased]

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

### Security

- Handled every `gosec` G104 unhandled-error finding explicitly and added
  `ReadHeaderTimeout` to every `http.Server` (G112). `make gosec` reports zero
  findings across `GOOS=linux/darwin/windows`; remaining findings are documented
  false positives in [docs/gosec-suppressions.md](docs/gosec-suppressions.md).
- Fixed a GitHub Actions shell-injection in `release.yml` (untrusted
  `workflow_dispatch` input now passed via `env`).
