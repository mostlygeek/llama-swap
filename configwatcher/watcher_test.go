package configwatcher

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatcher_DetectsModification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32
	w := NewWatcher(path, 100*time.Millisecond, func() {
		called.Add(1)
	})
	w.Start()
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	if err := os.WriteFile(path, []byte("updated content here"), 0644); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for called.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("onChange was not called after file modification")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestWatcher_DetectsSizeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("short"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32
	w := NewWatcher(path, 100*time.Millisecond, func() {
		called.Add(1)
	})
	w.Start()
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	if err := os.WriteFile(path, []byte("this is a much longer content string"), 0644); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for called.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("onChange was not called after file size change")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestWatcher_FileDeletedAndRecreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32
	w := NewWatcher(path, 100*time.Millisecond, func() {
		called.Add(1)
	})
	w.Start()
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	if err := os.WriteFile(path, []byte("recreated with different content"), 0644); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for called.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("onChange was not called after file recreation")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestWatcher_Stop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(path, 50*time.Millisecond, func() {})
	w.Start()
	w.Stop()
}

func TestWatcher_NoFalsePositives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("unchanged"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32
	w := NewWatcher(path, 100*time.Millisecond, func() {
		called.Add(1)
	})
	w.Start()
	defer w.Stop()

	time.Sleep(500 * time.Millisecond)

	if called.Load() != 0 {
		t.Fatalf("onChange was called %d times without any file change", called.Load())
	}
}
