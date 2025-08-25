package proxy

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path atomically.
// It writes to a temporary file in the same directory, fsyncs it,
// sets the desired mode, closes it, fsyncs the directory (best-effort),
// then renames over the destination.
//
// Mode preservation:
// - If the destination path already exists, its mode is preserved regardless of the provided mode.
// - If the destination does not exist and the provided mode is non-zero, that mode is used.
// - Otherwise, 0644 is used.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)

	// Determine effective mode
	effectiveMode := mode
	if fi, err := os.Stat(path); err == nil {
		effectiveMode = fi.Mode()
	} else {
		if effectiveMode == 0 {
			effectiveMode = 0o644
		}
	}

	// Create a temp file in the same directory for atomic rename
	tmpFile, err := os.CreateTemp(dir, ".tmp-config-*.yaml")
	if err != nil {
		return err
	}

	tmpName := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}

	// Ensure cleanup on any failure path
	defer func() {
		// If tmpFile still exists (e.g., rename failed), best-effort remove it
		_ = os.Remove(tmpName)
	}()

	// Write data
	if _, err = tmpFile.Write(data); err != nil {
		cleanup()
		return err
	}

	// Flush file contents
	if err = tmpFile.Sync(); err != nil {
		cleanup()
		return err
	}

	// Set mode
	if err = tmpFile.Chmod(effectiveMode); err != nil {
		cleanup()
		return err
	}

	// Close the temp file before rename
	if err = tmpFile.Close(); err != nil {
		cleanup()
		return err
	}

	// Best-effort fsync the directory before rename (not strictly required)
	if dirFD, err := os.Open(dir); err == nil {
		_ = dirFD.Sync()
		_ = dirFD.Close()
	}

	// Atomic rename
	if err = os.Rename(tmpName, path); err != nil {
		// best-effort cleanup
		_ = os.Remove(tmpName)
		return err
	}

	// Best-effort fsync the directory after rename to strengthen durability
	if dirFD, err := os.Open(dir); err == nil {
		_ = dirFD.Sync()
		_ = dirFD.Close()
	}

	return nil
}
