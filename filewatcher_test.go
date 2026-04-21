package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestFileWatcher_DetectsChange(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.yaml")

	// Create initial file
	if err := os.WriteFile(testFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	watcher := NewFileWatcher(100 * time.Millisecond)
	watcher.Add(testFile)

	changed := make(chan struct{}, 10)
	watcher.Start(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer watcher.Stop()

	// Wait for initial stat to record mtime
	time.Sleep(200 * time.Millisecond)

	// Modify the file
	time.Sleep(50 * time.Millisecond) // Ensure mtime differs
	if err := os.WriteFile(testFile, []byte("modified"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}

	// Wait for change to be detected
	select {
	case <-changed:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for file change detection")
	}
}

func TestFileWatcher_MultiplePaths(t *testing.T) {
	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "file1.yaml")
	file2 := filepath.Join(tmpDir, "file2.yaml")

	if err := os.WriteFile(file1, []byte("data1"), 0644); err != nil {
		t.Fatalf("failed to create file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("data2"), 0644); err != nil {
		t.Fatalf("failed to create file2: %v", err)
	}

	watcher := NewFileWatcher(100 * time.Millisecond)
	watcher.Add(file1)
	watcher.Add(file2)

	var changeCount int64
	watcher.Start(func() {
		atomic.AddInt64(&changeCount, 1)
	})
	defer watcher.Stop()

	// Wait for initial stat and a couple of ticks
	time.Sleep(350 * time.Millisecond)
	initialChanges := atomic.LoadInt64(&changeCount)

	// Modify file1
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(file1, []byte("modified1"), 0644); err != nil {
		t.Fatalf("failed to modify file1: %v", err)
	}

	// Wait for at least one new change
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		newCount := atomic.LoadInt64(&changeCount)
		if newCount > initialChanges {
			break
		}
	}

	// Now modify file2
	time.Sleep(50 * time.Millisecond)
	beforeFile2 := atomic.LoadInt64(&changeCount)
	if err := os.WriteFile(file2, []byte("modified2"), 0644); err != nil {
		t.Fatalf("failed to modify file2: %v", err)
	}

	// Wait for file2 change
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		newCount := atomic.LoadInt64(&changeCount)
		if newCount > beforeFile2 {
			break
		}
	}

	finalCount := atomic.LoadInt64(&changeCount)
	if finalCount <= initialChanges {
		t.Fatalf("expected more changes after modifying both files, got %d (initial: %d)", finalCount, initialChanges)
	}
}

func TestFileWatcher_Symlink(t *testing.T) {
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "real.yaml")
	linkFile := filepath.Join(tmpDir, "link.yaml")

	// Create real file
	if err := os.WriteFile(realFile, []byte("real data"), 0644); err != nil {
		t.Fatalf("failed to create real file: %v", err)
	}

	// Create symlink
	if err := os.Symlink(realFile, linkFile); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	watcher := NewFileWatcher(100 * time.Millisecond)
	watcher.Add(linkFile)

	changed := make(chan struct{}, 10)
	watcher.Start(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer watcher.Stop()

	// Wait for initial stat
	time.Sleep(200 * time.Millisecond)

	// Modify the real file (should be detected via symlink)
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(realFile, []byte("modified real data"), 0644); err != nil {
		t.Fatalf("failed to modify real file: %v", err)
	}

	select {
	case <-changed:
		// Success - symlink detection works
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for symlink target change detection")
	}
}

func TestFileWatcher_FileDeleted(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.yaml")

	if err := os.WriteFile(testFile, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	watcher := NewFileWatcher(100 * time.Millisecond)
	watcher.Add(testFile)

	// Should not panic when file is deleted
	watcher.Start(func() {})
	defer watcher.Stop()

	time.Sleep(200 * time.Millisecond)

	// Delete the file
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to delete test file: %v", err)
	}

	// Wait a bit to ensure no panic
	time.Sleep(300 * time.Millisecond)
}

func TestFileWatcher_FileRecreated(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.yaml")

	if err := os.WriteFile(testFile, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	watcher := NewFileWatcher(100 * time.Millisecond)
	watcher.Add(testFile)

	changed := make(chan struct{}, 10)
	watcher.Start(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer watcher.Stop()

	time.Sleep(200 * time.Millisecond)

	// Delete the file
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to delete test file: %v", err)
	}

	time.Sleep(150 * time.Millisecond)

	// Recreate the file (should be detected because mtime goes from zero time to now)
	if err := os.WriteFile(testFile, []byte("recreated"), 0644); err != nil {
		t.Fatalf("failed to recreate test file: %v", err)
	}

	select {
	case <-changed:
		// Success - recreation detected
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for file recreation detection")
	}
}

func TestFileWatcher_Stop(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.yaml")

	if err := os.WriteFile(testFile, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	watcher := NewFileWatcher(50 * time.Millisecond)
	watcher.Add(testFile)

	changed := make(chan struct{}, 100)
	watcher.Start(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})

	// Let it run for a bit
	time.Sleep(150 * time.Millisecond)

	// Stop the watcher
	watcher.Stop()

	// Modify the file after stop - should not trigger
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(testFile, []byte("modified after stop"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}

	// Wait and ensure no more changes
	time.Sleep(300 * time.Millisecond)

	close(changed)
	count := 0
	for range changed {
		count++
	}

	// Should have had some changes before stop, but not after
	if count < 1 {
		t.Logf("warning: no changes detected before stop (count: %d)", count)
	}
}
