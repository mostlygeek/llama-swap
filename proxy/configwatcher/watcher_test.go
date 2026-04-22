package configwatcher

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testInterval = 25 * time.Millisecond

// startWatcher launches w.Run in a goroutine and returns a function that
// cancels the context and waits for Run to return.
func startWatcher(t *testing.T, w *Watcher) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("watcher did not stop within 2s of cancel")
		}
	}
}

// waitForCount blocks until counter reaches want or timeout elapses.
func waitForCount(t *testing.T, counter *int64, want int64, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(counter) >= want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestWatcher_NoFireOnBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("a"), 0o644))

	var n int64
	stop := startWatcher(t, &Watcher{
		Path:     path,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()

	time.Sleep(testInterval * 5)
	require.Equal(t, int64(0), atomic.LoadInt64(&n), "baseline poll must not fire")
}

func TestWatcher_DetectsModTimeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("a"), 0o644))

	// Force a known baseline mtime.
	base := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(path, base, base))

	var n int64
	stop := startWatcher(t, &Watcher{
		Path:     path,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()

	// Let the baseline settle.
	time.Sleep(testInterval * 2)

	// Bump mtime well above the baseline so low-resolution filesystems still notice.
	require.NoError(t, os.Chtimes(path, base.Add(10*time.Second), base.Add(10*time.Second)))

	require.True(t, waitForCount(t, &n, 1, time.Second), "callback should fire after mtime change")
}

func TestWatcher_DetectsSizeChangeWithSameModTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("a"), 0o644))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	originalMtime := fi.ModTime()

	var n int64
	stop := startWatcher(t, &Watcher{
		Path:     path,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()
	time.Sleep(testInterval * 2)

	require.NoError(t, os.WriteFile(path, []byte("aaaaa"), 0o644))
	// Reset mtime back to the original so size is the only signal.
	require.NoError(t, os.Chtimes(path, originalMtime, originalMtime))

	require.True(t, waitForCount(t, &n, 1, time.Second), "callback should fire on size change")
}

func TestWatcher_SymlinkTargetSwap(t *testing.T) {
	dir := t.TempDir()
	targetA := filepath.Join(dir, "targetA")
	targetB := filepath.Join(dir, "targetB")
	link := filepath.Join(dir, "config.yaml")

	require.NoError(t, os.WriteFile(targetA, []byte("AAAA"), 0o644))
	require.NoError(t, os.WriteFile(targetB, []byte("BBBBBBBB"), 0o644))

	if err := os.Symlink(targetA, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation requires privilege on Windows: %v", err)
		}
		t.Fatalf("os.Symlink: %v", err)
	}

	var n int64
	stop := startWatcher(t, &Watcher{
		Path:     link,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()
	time.Sleep(testInterval * 2)

	// Atomic symlink swap (k8s ConfigMap pattern): create new symlink at a
	// temp name, then rename over the existing one.
	tmpLink := filepath.Join(dir, "config.yaml.tmp")
	require.NoError(t, os.Symlink(targetB, tmpLink))
	require.NoError(t, os.Rename(tmpLink, link))

	require.True(t, waitForCount(t, &n, 1, time.Second), "callback should fire after symlink target swap")
}

func TestWatcher_FileMissingThenReturns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("a"), 0o644))

	var n int64
	stop := startWatcher(t, &Watcher{
		Path:     path,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()
	time.Sleep(testInterval * 2)

	require.NoError(t, os.Remove(path))
	time.Sleep(testInterval * 3)
	require.Equal(t, int64(0), atomic.LoadInt64(&n), "removal alone must not fire")

	require.NoError(t, os.WriteFile(path, []byte("b"), 0o644))
	require.True(t, waitForCount(t, &n, 1, time.Second), "callback should fire when file returns")
}

func TestWatcher_ContextCancelStopsRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("a"), 0o644))

	w := &Watcher{Path: path, Interval: testInterval}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	time.Sleep(testInterval * 2)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancel")
	}
}
