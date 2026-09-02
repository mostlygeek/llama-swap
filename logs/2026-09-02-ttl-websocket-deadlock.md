# 2026-09-02 — Fix TTL_IgnoresWebsocket 10-minute CI deadlock

## Symptom / goal

Linux CI (`make test-all`) failed with a 10-minute timeout in
`internal/process`:

```
panic: close of closed channel
  ...TestProcessCommand_TTL_IgnoresWebsocket.func1 process_command_test.go:907
http: panic serving 127.0.0.1:...: close of closed channel
httptest.Server blocked in Close after 5 seconds, waiting for connections: ... in state active
panic: test timed out after 10m0s (TestProcessCommand_TTL_IgnoresWebsocket 9m49s)
```

Reproduced locally: `go test ./internal/process/` hung 120s+.

## Diagnosis

Two independently-pulled fork features collide:

- **The test** (upstream PR #1002) mock upstream treats *any non-`/health`*
  request as "the websocket": `close(websocketStarted)` then block on
  `<-releaseWebsocket`.
- **The fork's** dynamic-reasoning capability (upstream PR #915) added a
  `/props` reasoning-budget probe in `doStart`
  (`process_command.go:627` → `upstreamSupportsThinkingBudget`), fired when the
  command has no fixed reasoning budget (`simple-responder` has none).

Sequence:

1. Process starts, `/health` check passes.
2. `/props` probe → mock's non-`/health` branch → `close(websocketStarted)`
   early + a mock handler goroutine parks on `<-releaseWebsocket` (probe's 2s
   ctx times out client-side, but the server handler stays parked).
3. Test's real `/socket` websocket request → `close(websocketStarted)` again →
   `close of closed channel` panic (recovered by `net/http`), which severs the
   proxied response → `unexpected EOF` → the websocket `ServeHTTP` returns and
   `requestDone` closes.
4. Test fails at line 942 `t.Fatal("websocket request completed before it was
   released")`, which skips `close(releaseWebsocket)`.
5. `t.Cleanup` → `mock.Close()` blocks forever on the still-parked `/props`
   handler goroutine → 10-minute timeout.

## Change

`internal/process/process_command_test.go` — mock upstream blocks only on the
real websocket upgrade, answering all startup probes (`/health`, `/props`, …)
with `200` immediately, mirroring production's `swaputil.IsWebSocketUpgrade`:

```diff
-		if r.URL.Path == "/health" {
-			w.WriteHeader(http.StatusOK)
-			return
-		}
+		// Only the websocket upgrade blocks until released. Startup probes
+		// (the /health check and the /props reasoning-budget probe) must be
+		// answered immediately — otherwise they would trip the block-until-
+		// released dance and deadlock the server's Close on shutdown.
+		if !swaputil.IsWebSocketUpgrade(r) {
+			w.WriteHeader(http.StatusOK)
+			return
+		}
 		close(websocketStarted)
 		<-releaseWebsocket
 		w.WriteHeader(http.StatusOK)
```

Test-only; no production behavior changed. The `/props` probe now receives an
empty-body `200` (`build_info` absent → `reasoningDynamic=false`).

## Commands

- `make simple-responder`
- `go test -v -run TestProcessCommand_TTL_IgnoresWebsocket -count=5 -timeout 120s ./internal/process/` → 5/5 pass
- `go test ./internal/process/` → 46 pass (was 600s timeout)
- `make test-all` → all packages `ok` (`internal/process` 14.6s)
- `gofmt -w internal/process/process_command_test.go`
- `aidc-scan` → semgrep/gitleaks/gosec clean

## Verification

The previously-hanging test passes 5/5 and the full `make test-all` is green.

## Notes

The upstream repo never had to reconcile PR #1002 against PR #915; the fork
carries both, so this is a fork-integration bug. The fix hardens the test's mock
so any future startup probe (not just `/props`) is tolerated.
