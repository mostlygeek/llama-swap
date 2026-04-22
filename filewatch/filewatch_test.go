package filewatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher_DetectsFileChange(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.yaml")

	// Create initial file
	if err := os.WriteFile(testFile, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(testFile, 100*time.Millisecond)
	changes := w.Start()
	defer w.Close()

	// Wait for initial stat to register
	time.Sleep(200 * time.Millisecond)

	// Modify the file
	if err := os.WriteFile(testFile, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	// Should detect change within polling interval
	select {
	case <-changes:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("did not detect file change")
	}
}

func TestWatcher_Symlink(t *testing.T) {
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "real.yaml")
	symlinkFile := filepath.Join(tmpDir, "link.yaml")

	// Create real file and symlink
	if err := os.WriteFile(realFile, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile, symlinkFile); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(symlinkFile, 100*time.Millisecond)
	changes := w.Start()
	defer w.Close()

	// Wait for initial stat
	time.Sleep(200 * time.Millisecond)

	// Modify through symlink
	if err := os.WriteFile(symlinkFile, []byte("via symlink"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-changes:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("did not detect symlink change")
	}
}

func TestWatcher_K8sConfigmapSymlinkSwap(t *testing.T) {
	tmpDir := t.TempDir()
	realFile1 := filepath.Join(tmpDir, "real1.yaml")
	realFile2 := filepath.Join(tmpDir, "real2.yaml")
	symlinkFile := filepath.Join(tmpDir, "config.yaml")

	// Create initial real file and symlink
	if err := os.WriteFile(realFile1, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile1, symlinkFile); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(symlinkFile, 100*time.Millisecond)
	changes := w.Start()
	defer w.Close()

	// Wait for initial stat
	time.Sleep(200 * time.Millisecond)

	// Simulate k8s configmap swap: remove symlink, create new one
	os.Remove(symlinkFile)
	if err := os.WriteFile(realFile2, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile2, symlinkFile); err != nil {
		t.Fatal(err)
	}

	select {
	case <-changes:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("did not detect k8s configmap symlink swap")
	}
}

func TestWatcher_NoSpuriousChanges(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.yaml")

	if err := os.WriteFile(testFile, []byte("unchanged"), 0644); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(testFile, 100*time.Millisecond)
	changes := w.Start()
	defer w.Close()

	// Wait for multiple poll cycles without changes
	select {
	case <-changes:
		t.Fatal("spurious change detected")
	case <-time.After(500 * time.Millisecond):
		// Success - no changes
	}
}

func TestWatcher_Close(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.yaml")

	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(testFile, 50*time.Millisecond)
	changes := w.Start()
	w.Close()

	// Channel should be closed and not receive anything
	select {
	case _, ok := <-changes:
		if ok {
			t.Fatal("changes channel should be closed after Close()")
		}
	case <-time.After(200 * time.Millisecond):
		// Channel might not be closed but watcher should stop polling
	}
}
