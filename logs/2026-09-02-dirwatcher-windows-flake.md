# 2026-09-02 — Fix TestDirWatcher_MissingDirRecovers Windows CI failure

## Symptom / goal

Windows CI (`make test-all`) failed:

```
--- FAIL: TestDirWatcher_MissingDirRecovers (0.05s)
    dirwatcher_test.go:147:
      Received unexpected error:
      unlinkat C:\Users\RUNNER~1\AppData\Local\Temp\TestDirWatcher_MissingDirRecovers3921426038\001:
      The process cannot access the file because it is being used by another process.
FAIL github.com/mostlygeek/llama-swap/internal/watcher
```

## Diagnosis

`TestDirWatcher_MissingDirRecovers` removes the watched directory while the
`DirWatcher` goroutine is still running — intentional, to verify the watcher
recovers from a disappearing directory.

The watcher (`internal/watcher/dirwatcher.go`) polls every `testInterval`
(25 ms) and calls `os.ReadDir(dir)`, which briefly holds an open handle on the
directory. Windows refuses to unlink a path that another handle has open (Go
does not open the directory with `FILE_SHARE_DELETE`), so when the test's
`os.RemoveAll(dir)` coincides with a poll's `ReadDir`, it returns
`ERROR_SHARING_VIOLATION` → "being used by another process". POSIX `unlinkat`
allows unlinking an open path, so linux/darwin never hit this.

The watcher itself is correct — `scanDir` returns `exists=false` on a read
error and recovers on the next successful scan. The defect is purely the test's
`require.NoError` on a racy, retryable Windows removal.

## Change

`internal/watcher/dirwatcher_test.go`:

- Added `removeDirWithRetry(t, dir)` — retries `os.RemoveAll` up to 50× with a
  10 ms backoff (≤500 ms, 20× the poll interval), then asserts success.
- Replaced the mid-run `require.NoError(t, os.RemoveAll(dir))` at line 147 with
  `removeDirWithRetry(t, dir)`.

The watcher holds the directory handle only for the microseconds of a `ReadDir`
call, so a retry quickly lands in a gap. On non-Windows the first attempt
succeeds, so behavior is unchanged. The other `os.Remove` calls in the package
target single files (not the directory) and don't hold the `ReadDir` handle, so
they were left as-is.

## Commands

- `gofmt -w internal/watcher/dirwatcher_test.go`
- `GOOS=windows go vet ./internal/watcher/` → ok
- `go test -run TestDirWatcher -count=3 -timeout 60s ./internal/watcher/` → 24 pass
- `go test ./internal/watcher/` → pass

## Verification

Watcher tests pass on the linux dev host (24 tests, run 3×) and cross-compile /
vet clean for Windows. The Windows-specific race could not be reproduced on
linux (POSIX unlink semantics); the fix targets the exact reported error and is
zero-cost on other platforms.

## Notes

Test-only change. No production watcher behavior modified.
