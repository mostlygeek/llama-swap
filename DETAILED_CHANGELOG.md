# Detailed Changelog

Long-form, dated entries — what / why / how / verification — newest first.
High-level summaries live in [CHANGELOG.md](CHANGELOG.md).

---

## 2026-09-02 — Maximal upstream integration (re-established on upstream `7a14664`)

### What

Rebuilt the fork on top of upstream `7a14664` (2026-08-31) instead of merging 96
divergent upstream commits into a heavily-rewritten tree. Upstream's new
scheduler, selectors, profiles, peer namespaces, `internal/matrix`,
`internal/hw`, ComfyUI, sqlite `internal/store`, and docagent/mcptools are
inherited natively; the fork's differentiators (Anthropic `apiconv`, Ollama
compat, vanilla `ui_dist` UI, passthrough config, gosec hardening, guardrails)
were re-applied on top as a curated patch series.

### Why

A straight merge was infeasible: nearly every upstream commit collided with the
files the fork rewrote (`server.go` routes, config, router), and upstream's
naming refactor (#1003) touched symbols referenced throughout the fork.

### How / verification

Phase-by-phase, each verified with `go build ./...` + targeted `go test` before
advancing; full `go test ./internal/...` green (20 packages). Adopted upstream's
sqlite store (dropped the file-based metrics store). See the session log
`logs/2026-09-02-upstream-max-integration.md` for the per-phase diff, commands,
and the gosec baseline/oracle.

### Notes

- Selectors/profiles/capabilities are **API-only** on this branch (the vanilla
  UI has no screens for them yet) — deferred follow-up.
- The `/stats` page aggregates over the most recent ≤999 activity rows
  (server-side limit) rather than all-time — deferred rewire onto
  `/api/metrics/stats`.

---

## 2026-07-04 — Router swap deadlock on TTL unload race (+ failed-start variant, TTL first-tick unload)

### What

Fixed a permanent whole-router deadlock in the swap path and two adjacent
defects in the process state machine:

1. A request arriving while its target model was mid-TTL-unload
   (`StateStopping`) wedged the router forever.
2. A failed model start (crash on load, health-check timeout) had roughly
   even odds of wedging the router the same way, depending on channel
   receive ordering.
3. A model reloaded after an idle gap longer than its `ttl` inherited a
   stale idle timestamp and could be unloaded again by the TTL goroutine's
   first 1-second tick, before the request that triggered the reload was
   served.

Changed files: `internal/process/process.go`,
`internal/process/process_command.go`, `internal/router/base.go`, tests in
`internal/process/process_command_test.go`, `internal/router/helpers_test.go`.

### Why (incident)

2026-07-04, production host "bigdumbo": `gemma4-31b-mtp` (ttl 600) hit its
TTL at 05:53:30 IST; an opencode request for the same model arrived ~100 ms
later. From that moment every `/v1/chat/completions` request — for any model
— hung until client timeout and was logged as `200` with a 0-byte body
(`metrics: empty body, recording minimal metrics`), `/running` returned
`[]`, and the GPU sat idle with no upstream processes. The client's 60-min
timeout + instant retry produced an hourly recurrence until the service was
restarted. Ops-side RCA log:
`machine-config/logs/bigdumbo/2026-07-04-llama-swap-ttl-swap-wedge-rca-fix.md`.

### Root cause

`baseRouter.doSwap` composed three non-atomic process operations:

```go
if target.State() == process.StateStopped {   // snapshot
    go func() { target.Run(timeout) }()       // maybe start
}
err := target.WaitReady(b.shutdownCtx)        // wait
```

During a TTL unload the snapshot observes `StateStopping`, so `Run()` is
skipped; the `WaitReady` send then pends while `run()` is inside its
`stopCh` case and is only received after the transition to `StateStopped`,
where it was appended to a parked-waiter list (`readyWaiters`). Parked
waiters were only woken by a future `Run()` resolution — and the only
`Run()` caller was the very `doSwap` that had already declined to call it.
The swap goroutine therefore never reported `swapDone`; its entry in the
router's `active` map never cleared; same-model requests joined the dead
swap and requests for other models queued behind it via eviction collision.

The failed-start variant is the same parking defect with a different
trigger: `go Run()` and `WaitReady()` race to `run()`'s select; when `runCh`
is received first and the start fails, the failure notification fires while
the `WaitReady` send is still blocked on the channel — it parks afterwards,
unwakeable.

Key insight: **no notify-on-transition scheme can fix this** — a sender
still blocked on the channel when the transition fires always parks after
the notification. The parked-waiter state itself had to go.

### How

- `internal/process/process_command.go`:
  - `runReq` gained a buffered `ready chan error`. `run()` answers it at
    every start resolution: success (`nil`), start failure, stop-during-
    start (`ErrStartAborted`), parent-context shutdown. A `runReq` received
    while already `StateReady` answers `ready` with `nil` (ensure semantics:
    already-ready is success, not a conflict).
  - New `EnsureReady(ctx, timeout)` sends that request and waits on `ready`.
    Because the start decision happens inside `run()` against live state, a
    request that pends during an in-flight Stop is received afterwards at
    `StateStopped` and simply starts the process.
  - `WaitReady` answers immediately from current state — `nil` when ready,
    shutdown error, or wraps the new `ErrNotStarted` sentinel. The
    `readyWaiters`/`notifyWaiters` machinery is deleted. (In the main select
    the state is only ever `Stopped` or `Ready`; sends made while a start or
    stop is resolving pend on the channel and get the post-resolution
    answer.)
  - `lastUse` is set to now on successful start — becoming ready counts as
    use for the TTL idle clock.
  - `case <-cmdDone:` (unexpected upstream exit) logs at Warn when no `Run`
    caller is parked; with the router on `EnsureReady`, nothing else would
    surface it.
  - `Run` keeps its contract (blocks until termination) for API
    compatibility; `EnsureReady` callers do not park a termination response.
- `internal/router/base.go`: `doSwap` stops evictions as before, then calls
  `b.processes[modelID].EnsureReady(b.shutdownCtx, timeout)` and reports the
  result on `swapDoneCh`. Failed swaps now propagate a real error to every
  waiter (`SendError`) instead of hanging them.
- `internal/process/process.go`: interface gains `EnsureReady`; `WaitReady`
  contract documented as fail-fast.
- Tests: router `fakeProcess` implements `EnsureReady` (preserving the
  `runCalls`/`runStarted`/`readyCh` hooks existing tests observe);
  `runAsync` helper retries `ErrNotStarted` while its `Run` goroutine
  registers.

### Verification

Toolchain: go 1.26.4 darwin/arm64; `make simple-responder` prebuilt.

```
go test -race -count=1 ./internal/process/    20 passed
go test -race -count=1 ./internal/router/    102 passed
GOOS=linux GOARCH=amd64 go build ./...       OK
make gosec                                   0 issues (73 files, 3 GOOS)
gofmt / go vet                               clean
```

New regression tests (`internal/process/process_command_test.go`):

- `TestProcessCommand_EnsureReadyDuringStop` — reproduces the incident: an
  upstream started with `-ignore-sig-term` holds `run()` in its stop case
  for a ~1.5 s graceful window; `EnsureReady` issued mid-stop rode it out
  and had the model restarted and serving in 1.74 s. Pre-fix behaviour:
  parked forever.
- `TestProcessCommand_EnsureReadyStartFailure` — failing command reports an
  error promptly (pre-fix: ~50 % permanent park).
- `TestProcessCommand_EnsureReady` — start + ready + idempotent re-call.
- `TestProcessCommand_WaitReadyNotStarted` — fail-fast `ErrNotStarted`.
- `TestProcessCommand_StartResetsLastUse` — TTL idle clock reset on start.

Known flake (pre-existing, unrelated): under whole-repo parallel load the
bash-wrapper tests `TestProcessCommand_StopForkingWrapper` /
`TestProcessCommand_StopHonorsGracefulTimeout` can miss their 3 s startup
budgets; verified identical on the unpatched tree and 3/3 passes in
isolation.

### Notes

- The two semgrep findings on the touched files are the long-standing,
  documented G204 subprocess sites (`docs/gosec-suppressions.md`) — running
  operator-configured model commands is llama-swap's purpose; untouched by
  this change.
- Candidate for upstreaming: upstream `mostlygeek/llama-swap` (post-#790
  backend, divergence baseline `ccfba0d`) contains the same
  snapshot+`Run`+`WaitReady` composition in `doSwap` and the same parking
  `WaitReady`, so the deadlock class applies there too.
