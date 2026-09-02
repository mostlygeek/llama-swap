# Detailed Changelog

Long-form, dated entries — what / why / how / verification — newest first.
High-level summaries live in [CHANGELOG.md](CHANGELOG.md).

---

## 2026-09-02 — Fix dead close buttons in the activity capture dialog

### What

On `/activity`, opening an entry's capture ("View") shows a modal whose close
controls (`×` in the header, `Close` in the footer) did nothing. The dialog
could only be dismissed via Escape or a backdrop click.

### Why

`captureDialog.js`'s `render()` produced two `[data-close]` buttons but wired
the click handler with `dlg.querySelector("[data-close]")`, which returns only
the first match. Depending on the branch, one button — or, in the
capture-not-found path, both — ended up without a handler.

### How

Changed the single `querySelector` bind to
`dlg.querySelectorAll("[data-close]").forEach(...)` so every close button in the
rendered dialog gets the `close` handler.

```diff
-    dlg.querySelector("[data-close]")?.addEventListener("click", close);
+    dlg.querySelectorAll("[data-close]").forEach((btn) => btn.addEventListener("click", close));
```

### Commands

- `aidc-scan` → semgrep + gitleaks clean; all language scanners skipped (only
  a hand-authored JS file under `internal/server/ui_dist/` changed, no build
  step).

### Verification

Reviewed the rendered markup: both the header `×` and footer `Close` now match
the `[data-close]` selector and receive the handler.

### Notes

The web UI under `internal/server/ui_dist/` is vanilla ES-module JS served
directly with no build step, so no rebuild was required.

---

## 2026-09-02 — Fix `GOOS=windows gosec` build failure in vllm-wrapper

### What

`make gosec` failed its `GOOS=windows` pass — not on a finding, but on a compile
error: `cmd/vllm-wrapper/main.go:233:21: undefined: syscall.Kill`.

### Why

`syscall.Kill` is only defined on Unix-like platforms. The `sleep` subcommand
used it to send `SIGTERM` to the serve proxy PID after vLLM enters sleep mode.
Windows has no `syscall.Kill`, so the package would not compile under
`GOOS=windows`, and gosec aborts the whole target when a package fails to build.
(`syscall.SIGTERM` in the signal-handling path is fine — that constant is defined
on Windows; only `Kill` is missing.)

### How

Extracted the stop call behind a small `stopProcess(pid int) error` helper split
across build-tagged files:

- `cmd/vllm-wrapper/stop_unix.go` (`//go:build !windows`) → `syscall.Kill(pid, syscall.SIGTERM)`
  (unchanged graceful behavior on Linux/macOS).
- `cmd/vllm-wrapper/stop_windows.go` (`//go:build windows`) → `os.FindProcess(pid).Kill()`
  (Windows has no SIGTERM). This wrapper targets Linux/systemd deployments; the
  Windows build exists only to keep cross-compilation and gosec green.

`main.go` line 233 now calls `stopProcess(stopPID)`.

### Commands

- `GOOS=windows go build ./cmd/vllm-wrapper/` → ok
- `GOOS=linux go build ./cmd/vllm-wrapper/` → ok
- `go test ./cmd/vllm-wrapper/` → 5 passed
- `make gosec` → linux/darwin/windows all report `Issues: 0`, no build errors
- `aidc-scan` → clean

### Notes

Behavior on the primary Linux path is identical (still `SIGTERM`). The Windows
variant is a hard kill because the platform offers no graceful-termination
signal; acceptable given the wrapper is not deployed on Windows.

---

## 2026-09-02 — Fix TestDirWatcher_MissingDirRecovers Windows CI flake

### What

Windows CI (`make test-all`) failed:

```
--- FAIL: TestDirWatcher_MissingDirRecovers (0.05s)
    dirwatcher_test.go:147: Received unexpected error:
      unlinkat C:\...\TestDirWatcher_MissingDirRecovers.../001:
      The process cannot access the file because it is being used by another process.
```

### Why

The test removes the watched directory *while the `DirWatcher` goroutine is
running* (by design — it verifies the watcher survives a disappearing dir). The
watcher polls every 25 ms via `os.ReadDir`, which briefly holds an open handle
on the directory. Windows refuses to unlink a path another handle has open
(no `FILE_SHARE_DELETE`), so when the test's `os.RemoveAll(dir)` lands in the
same instant as a poll's `ReadDir`, it returns a sharing violation. POSIX
`unlinkat` has no such restriction, so linux/darwin never hit it.

### How

Added a `removeDirWithRetry` test helper that retries `os.RemoveAll` up to 50×
with a 10 ms backoff (≤500 ms, 20× the poll interval) and used it at the mid-run
removal site. The watcher's handle is only held for the microseconds of a
`ReadDir` call, so a retry quickly finds a gap. First attempt succeeds on
non-Windows, so behavior there is unchanged. Test-only; the watcher itself
already handles a missing directory correctly (`scanDir` returns
`exists=false`).

Other removals in the watcher tests operate on single files, not the directory,
so they don't hold the directory handle that `ReadDir` does and were left as-is.

### Commands

- `gofmt -w internal/watcher/dirwatcher_test.go`
- `GOOS=windows go vet ./internal/watcher/` → ok
- `go test -run TestDirWatcher -count=3 ./internal/watcher/` → 24 pass
- `go test ./internal/watcher/` → pass

### Notes

Could not reproduce on the linux dev host (POSIX semantics); the fix targets the
exact Windows error and is a no-op cost on other platforms.

---

## 2026-09-02 — Fix TTL_IgnoresWebsocket deadlock (fork feature collision)

### What

`TestProcessCommand_TTL_IgnoresWebsocket` (`internal/process`) intermittently
deadlocked in CI, tripping the 10-minute test timeout and failing `make
test-all` / `make test-dev`. The trace showed a `panic: close of closed channel`
at `process_command_test.go:907` and a `httptest.Server blocked in Close` on an
active connection.

### Why

A collision between two independently-pulled features:

- The test (upstream PR #1002) uses a mock upstream that treats *any non-`/health`
  request* as "the websocket": it `close(websocketStarted)`s and then blocks on
  `<-releaseWebsocket`.
- The fork's dynamic-reasoning capability (upstream PR #915) added a `/props`
  reasoning-budget probe in `doStart` (`upstreamSupportsThinkingBudget`, fired
  when the command has no fixed budget — which the test's `simple-responder`
  does not).

At startup the `/props` probe hit the mock's non-`/health` branch, closing
`websocketStarted` early and leaving a mock handler goroutine parked on
`<-releaseWebsocket`. Then the test's real websocket request called
`close(websocketStarted)` again → `close of closed channel` panic (recovered by
`net/http`, but it severed the proxied response → `unexpected EOF` → the request
returned early). The test then failed at line 942
(`websocket request completed before it was released`) via `t.Fatal`, which
skipped `close(releaseWebsocket)`; the `t.Cleanup` `mock.Close()` then blocked
forever on the still-parked `/props` goroutine → 10-minute timeout.

### How

Changed the mock upstream to block only on the *actual* websocket upgrade,
answering every startup probe (`/health`, `/props`, anything else) with `200`
immediately — mirroring production's `swaputil.IsWebSocketUpgrade` check:

```go
if !swaputil.IsWebSocketUpgrade(r) {
    w.WriteHeader(http.StatusOK)
    return
}
close(websocketStarted)
<-releaseWebsocket
w.WriteHeader(http.StatusOK)
```

The `/props` probe now gets an empty-body `200` (`build_info` absent →
`reasoningDynamic=false`), so it no longer parks a goroutine, and only the real
`/socket` upgrade drives the block-until-released sequence the test asserts on.
Test-only change; no production behavior touched.

### Commands

- `make simple-responder` (test needs the helper binary)
- `go test -v -run TestProcessCommand_TTL_IgnoresWebsocket -count=5 ./internal/process/` → 5/5 pass
- `go test ./internal/process/` → 46 pass (was a 600s timeout)
- `make test-all` → all packages `ok`
- `gofmt -w internal/process/process_command_test.go`; `aidc-scan` → clean

### Notes

Root cause is the fork carrying both upstream PR #1002 (the test) and PR #915
(the `/props` probe) that upstream did not have to reconcile against each other.
The fix hardens the test's mock so any future startup probe is tolerated too.

---

## 2026-09-02 — Suppress DXGI COM interop gosec false positives (Windows)

### What

CI `gosec` (pinned to `v2.26.1` in `.github/workflows/gosec.yml`) reported 12
`GOOS=windows` findings in `internal/hw/dxgi_windows.go` — 5×G115 (integer
overflow) and 7×G103 (`unsafe.Pointer`). The local `make gosec` uses an older
`gosec` (`dev`) that predates the G115 rule, so these were invisible locally.

### Why

Both classes are false positives inherent to DXGI COM interop:

- **G115 (HRESULT/LUID):** an `HRESULT` is a 32-bit status code returned from a
  syscall as `uintptr`. Truncating it to `uint32`/`int32` and testing the sign
  bit is the documented Win32 `SUCCEEDED`/`FAILED` semantics, not an overflow.
  The `luid:%08x` identity likewise reinterprets the fixed 32-bit `AdapterLUID`
  ABI field for display.
- **G103 (`unsafe.Pointer`):** mandatory to pass the COM factory/adapter vtable
  pointers and `DXGI_ADAPTER_DESC` struct across the `syscall.SyscallN` /
  `LazyProc.Call` boundary. Same by-design verdict as the existing
  `pdh_windows.go` / `d3dkmt_windows.go` sites.

### How

Added inline `// #nosec G115 -- …` / `// #nosec G103 -- …` markers at the exact
flagged lines (4 G115 markers — one covers the two conversions on the
`hresultFailed` line — and 7 G103 markers), matching the established style. No
code was restructured to dodge the scanner. Updated the audit ledger
`docs/gosec-suppressions.md`: summary totals (G115 25→29, G103 20→27, total
78→89) and the G115/G103 section prose to name `dxgi_windows.go`. The
`TestNosecLedgerInSync` guard enforces the marker/ledger counts stay in sync.

### Commands

- `go install github.com/securego/gosec/v2/cmd/gosec@v2.26.1` (match CI)
- `GOOS=windows gosec ./internal/hw/` → `Issues: 0`
- `GOOS={linux,darwin,windows} gosec ./...` → all `Issues: 0`, no build errors
- `GOOS=windows go build ./internal/hw/` → ok
- `gofmt -w internal/hw/dxgi_windows.go`
- `go test ./internal/audit/` → `TestNosecLedgerInSync` passes
- `aidc-scan` → clean

### Notes

`make gosec` locally still runs the `dev` gosec, which under-reports vs CI. The
verification above installed the CI-pinned `v2.26.1` explicitly to reproduce and
confirm the fix. Consider pinning the same version in the Makefile as a
follow-up so local and CI agree.

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
