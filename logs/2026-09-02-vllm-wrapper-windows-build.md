# 2026-09-02 — Fix vllm-wrapper Windows build in `make gosec`

## Symptom / goal

`make gosec` failed. The `GOOS=linux` and `GOOS=darwin` passes reported
`Issues: 0`, but the `GOOS=windows` pass aborted with a Go compile error:

```
Golang errors in file: [/workspace/cmd/vllm-wrapper/main.go]:
  > [line 233 : column 21] - undefined: syscall.Kill
```

## Diagnosis

`cmd/vllm-wrapper/main.go` used `syscall.Kill(stopPID, syscall.SIGTERM)` in the
`sleep` subcommand to terminate the serve proxy after vLLM enters sleep mode.
`syscall.Kill` is only defined on Unix-like platforms, so the package would not
compile under `GOOS=windows`. gosec aborts a whole target when a package fails
to build, hence the failure.

`grep -n 'syscall\.' cmd/vllm-wrapper/main.go` showed three uses:
`syscall.SIGTERM` (lines 172, 176) and `syscall.Kill` (line 233). Only `Kill` is
Unix-only — `syscall.SIGTERM` compiles on Windows — so the fix only needed to
isolate the `Kill` call.

## Change

Extracted the stop call behind a build-tagged `stopProcess(pid int) error`
helper.

`main.go` (line 233):
```diff
-	if stopPID > 0 {
-		if err := syscall.Kill(stopPID, syscall.SIGTERM); err != nil {
-			log.Fatalf("Failed to stop serve proxy process %d: %v", stopPID, err)
-		}
-		log.Printf("Sent SIGTERM to serve proxy process %d", stopPID)
-	}
+	if stopPID > 0 {
+		if err := stopProcess(stopPID); err != nil {
+			log.Fatalf("Failed to stop serve proxy process %d: %v", stopPID, err)
+		}
+		log.Printf("Sent stop signal to serve proxy process %d", stopPID)
+	}
```

New `cmd/vllm-wrapper/stop_unix.go` (`//go:build !windows`):
```go
func stopProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}
```

New `cmd/vllm-wrapper/stop_windows.go` (`//go:build windows`):
```go
func stopProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
```

## Commands

- `GOOS=windows go build ./cmd/vllm-wrapper/` → `WINDOWS BUILD OK`
- `GOOS=linux go build ./cmd/vllm-wrapper/` → `LINUX BUILD OK`
- `gofmt -l cmd/vllm-wrapper/` → no output (formatted)
- `go test ./cmd/vllm-wrapper/` → 5 passed
- `make gosec` → linux/darwin/windows all `Issues: 0`, no build errors
- `aidc-scan` → semgrep/gitleaks/gosec clean, rest skipped (no matching files)

## Verification

`make gosec` now completes all three GOOS targets with `Issues: 0` and no Golang
build errors. Package builds for both windows and linux; existing tests pass.

## Notes

The Linux/macOS path keeps identical behavior (graceful `SIGTERM`). Windows has
no SIGTERM, so its variant uses `os.FindProcess(pid).Kill()` (hard kill). This
is acceptable because the wrapper targets Linux/systemd deployments (it shells
out to `journalctl`); the Windows build exists only to keep cross-compilation
and gosec green.
