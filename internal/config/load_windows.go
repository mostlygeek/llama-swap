//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateStorePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("store.path must not be empty")
	}

	info, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("store.path: %w", err)
		}
		// File does not exist; ensure the parent directory is writable by
		// probing it with a temporary file. os.Access is unreliable on
		// Windows because it only reflects the read-only attribute.
		dir := filepath.Dir(path)
		if err := checkDirWritableWindows(dir); err != nil {
			return fmt.Errorf("store.path: directory %s is not writable: %w", dir, err)
		}
		return nil
	}

	// File exists; ensure it is a regular file and is writable.
	if info.IsDir() {
		return fmt.Errorf("store.path: %s is a directory, not a file", path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("store.path: %s is not writable: %w", path, err)
	}
	f.Close()
	return nil
}

func checkDirWritableWindows(dir string) error {
	tmp, err := os.CreateTemp(dir, ".llama-swap-write-test-*")
	if err != nil {
		return err
	}
	tmp.Close()
	return os.Remove(tmp.Name())
}
