package configwatcher

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startDirWatcher launches w.Run in a goroutine and returns a function that
// cancels the context and waits for Run to return.
func startDirWatcher(t *testing.T, w *DirWatcher) func() {
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
			t.Fatal("DirWatcher did not stop within 2s of cancel")
		}
	}
}

func writeYAMLInDir(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func TestDirWatcher_NoFireOnBaseline(t *testing.T) {
	dir := t.TempDir()
	writeYAMLInDir(t, dir, "a.yaml", "a")

	var n int64
	stop := startDirWatcher(t, &DirWatcher{
		Path:     dir,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()

	time.Sleep(testInterval * 5)
	require.Equal(t, int64(0), atomic.LoadInt64(&n), "baseline poll must not fire")
}

func TestDirWatcher_DetectsFileAdd(t *testing.T) {
	dir := t.TempDir()
	writeYAMLInDir(t, dir, "a.yaml", "a")

	var n int64
	stop := startDirWatcher(t, &DirWatcher{
		Path:     dir,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()
	time.Sleep(testInterval * 2)

	writeYAMLInDir(t, dir, "b.yaml", "b")
	require.True(t, waitForCount(t, &n, 1, time.Second), "callback should fire when a file is added")
}

func TestDirWatcher_DetectsFileRemoval(t *testing.T) {
	dir := t.TempDir()
	writeYAMLInDir(t, dir, "a.yaml", "a")
	writeYAMLInDir(t, dir, "b.yaml", "b")

	var n int64
	stop := startDirWatcher(t, &DirWatcher{
		Path:     dir,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()
	time.Sleep(testInterval * 2)

	require.NoError(t, os.Remove(filepath.Join(dir, "b.yaml")))
	require.True(t, waitForCount(t, &n, 1, time.Second), "callback should fire when a file is removed")
}

func TestDirWatcher_DetectsModTimeChange(t *testing.T) {
	dir := t.TempDir()
	writeYAMLInDir(t, dir, "a.yaml", "a")

	base := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "a.yaml"), base, base))

	var n int64
	stop := startDirWatcher(t, &DirWatcher{
		Path:     dir,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()
	time.Sleep(testInterval * 2)

	require.NoError(t, os.Chtimes(filepath.Join(dir, "a.yaml"), base.Add(10*time.Second), base.Add(10*time.Second)))
	require.True(t, waitForCount(t, &n, 1, time.Second), "callback should fire after mtime change")
}

func TestDirWatcher_IgnoresNonYAMLFiles(t *testing.T) {
	dir := t.TempDir()
	writeYAMLInDir(t, dir, "a.yaml", "a")

	var n int64
	stop := startDirWatcher(t, &DirWatcher{
		Path:     dir,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()
	time.Sleep(testInterval * 2)

	// Adding a .txt file must not fire.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644))
	time.Sleep(testInterval * 4)
	require.Equal(t, int64(0), atomic.LoadInt64(&n), "non-YAML files must be ignored")

	// Adding a .yml file must fire.
	writeYAMLInDir(t, dir, "b.yml", "b")
	require.True(t, waitForCount(t, &n, 1, time.Second), "callback should fire for *.yml files")
}

func TestDirWatcher_MissingDirRecovers(t *testing.T) {
	dir := t.TempDir()
	writeYAMLInDir(t, dir, "a.yaml", "a")

	var n int64
	stop := startDirWatcher(t, &DirWatcher{
		Path:     dir,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()
	time.Sleep(testInterval * 2)

	// Remove the directory. No fire expected on disappearance alone.
	require.NoError(t, os.RemoveAll(dir))
	time.Sleep(testInterval * 3)
	require.Equal(t, int64(0), atomic.LoadInt64(&n), "directory removal alone must not fire")

	// Recreate the directory and a YAML file; the recovery should fire.
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writeYAMLInDir(t, dir, "recovered.yaml", "r")
	require.True(t, waitForCount(t, &n, 1, time.Second), "callback should fire when dir returns with content")
}

func TestDirWatcher_EmptyDirSuppressedThenRecovers(t *testing.T) {
	// Present-with-content → empty (all YAML removed, dir still exists)
	// must stay quiet — treated as transient per the documented policy.
	// The transition back to content fires.
	dir := t.TempDir()
	writeYAMLInDir(t, dir, "a.yaml", "a")

	var n int64
	stop := startDirWatcher(t, &DirWatcher{
		Path:     dir,
		Interval: testInterval,
		OnChange: func() { atomic.AddInt64(&n, 1) },
	})
	defer stop()
	time.Sleep(testInterval * 2)

	// Remove the only YAML file. Dir still exists but is empty of YAML.
	require.NoError(t, os.Remove(filepath.Join(dir, "a.yaml")))
	time.Sleep(testInterval * 4)
	require.Equal(t, int64(0), atomic.LoadInt64(&n), "emptying the directory must not fire")

	// Add a YAML file back; transition to present-with-content fires.
	writeYAMLInDir(t, dir, "c.yaml", "c")
	require.True(t, waitForCount(t, &n, 1, time.Second), "callback should fire when content returns")
}

func TestDirWatcher_ContextCancelStopsRun(t *testing.T) {
	dir := t.TempDir()
	writeYAMLInDir(t, dir, "a.yaml", "a")

	w := &DirWatcher{Path: dir, Interval: testInterval}
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
