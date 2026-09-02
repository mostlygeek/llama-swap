# Changelog

All notable changes to this fork are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Entries track this fork's deltas on top of the upstream base (see the fork
notice in [README.md](README.md); divergence baseline: upstream `ccfba0d`).
Long-form entries with full context live in
[DETAILED_CHANGELOG.md](DETAILED_CHANGELOG.md).

## [Unreleased]

### Fixed

- Router swap deadlock: a request racing a TTL unload — or a failed model
  start — could park a readiness waiter that nothing would ever wake,
  permanently wedging the whole router (all models, until restart). The swap
  path now makes the start decision atomically inside the process state
  machine via the new `Process.EnsureReady`, and `WaitReady` fails fast with
  `ErrNotStarted` instead of parking. Root cause of the 2026-07-04 bigdumbo
  production wedge.
- TTL idle clock now resets when a process becomes ready, so a model reloaded
  after an idle gap longer than its `ttl` is no longer eligible for unload on
  the TTL goroutine's first tick.

### Added

- `Process.EnsureReady(ctx, timeout)`: start-if-stopped plus wait-until-ready
  as a single state-machine request; new `ErrNotStarted` sentinel.
- Warn-level log when an upstream process exits unexpectedly (previously
  debug-only once no `Run` caller was parked to surface it).
