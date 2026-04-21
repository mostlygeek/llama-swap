package proxy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func watchedFile(t *testing.T, initial string) string {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(initial), 0644))
	return path
}

func TestFileWatcher_detectsChange(t *testing.T) {
	path := watchedFile(t, "initial")
	fw := NewFileWatcher(path, 100*time.Millisecond)
	ch := fw.Start()
	defer fw.Stop()

	// Initial stat may or may not emit; clear any pending events.
	select {
	case <-ch:
	default:
	}

	time.Sleep(200 * time.Millisecond)
	require.NoError(t, os.WriteFile(path, []byte("changed"), 0644))

	select {
	case <-ch:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("expected file change notification")
	}
}

func TestFileWatcher_noFalsePositive(t *testing.T) {
	path := watchedFile(t, "initial")
	fw := NewFileWatcher(path, 100*time.Millisecond)
	ch := fw.Start()
	defer fw.Stop()

	select {
	case <-ch:
	default:
	}

	time.Sleep(300 * time.Millisecond)

	select {
	case <-ch:
		t.Fatal("unexpected file change notification")
	case <-time.After(200 * time.Millisecond):
		// success
	}
}

func TestFileWatcher_followsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping symlink test on windows")
	}

	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.yaml")
	linkPath := filepath.Join(dir, "link.yaml")

	require.NoError(t, os.WriteFile(realPath, []byte("initial"), 0644))
	require.NoError(t, os.Symlink(realPath, linkPath))

	fw := NewFileWatcher(linkPath, 100*time.Millisecond)
	ch := fw.Start()
	defer fw.Stop()

	select {
	case <-ch:
	default:
	}

	time.Sleep(200 * time.Millisecond)
	require.NoError(t, os.WriteFile(realPath, []byte("changed"), 0644))

	select {
	case <-ch:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("expected change through symlink")
	}
}

func TestFileWatcher_missingFileThenAppears(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.yaml")

	fw := NewFileWatcher(path, 100*time.Millisecond)
	ch := fw.Start()
	defer fw.Stop()

	select {
	case <-ch:
	default:
	}

	time.Sleep(200 * time.Millisecond)
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0644))

	select {
	case <-ch:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("expected change when file appears")
	}
}

func TestFileWatcher_repeatedChangesOnlyOneEvent(t *testing.T) {
	path := watchedFile(t, "initial")
	fw := NewFileWatcher(path, 50*time.Millisecond)
	ch := fw.Start()
	defer fw.Stop()

	select {
	case <-ch:
	default:
	}

	for i := 0; i < 3; i++ {
		require.NoError(t, os.WriteFile(path, []byte("change"), 0644))
		time.Sleep(60 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	events := 0
loop:
	for {
		select {
		case <-ch:
			events++
		default:
			break loop
		}
	}

	// Because the channel is buffered to 1, rapid changes
	// should coalesce into at most a few notifications.
	assert.LessOrEqual(t, events, 2, "expected events to coalesce")
}

func TestFileWatcher_Stop(t *testing.T) {
	path := watchedFile(t, "initial")
	fw := NewFileWatcher(path, 50*time.Millisecond)
	_ = fw.Start()
	fw.Stop()

	// After Stop, writing should not panic and we should not leak.
	require.NoError(t, os.WriteFile(path, []byte("changed"), 0644))
}
