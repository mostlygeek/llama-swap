//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateStorePath(path string) error {
	return validateDatabasePath(path, "store.path")
}

func validateStoreCapturesPath(path string) error {
	return validateDatabasePath(path, "store.captures.path")
}

// validateDatabasePath checks a SQLite file path; key names the setting in
// error messages.
func validateDatabasePath(path string, key string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s must not be empty", key)
	}

	info, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("%s: %w", key, err)
		}
		// File does not exist; ensure the parent directory is writable by
		// probing it with a temporary file. os.Access is unreliable on
		// Windows because it only reflects the read-only attribute. A missing
		// directory is created when the store opens.
		dir := filepath.Dir(path)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return nil
		}
		if err := checkDirWritableWindows(dir); err != nil {
			return fmt.Errorf("%s: directory %s is not writable: %w", key, dir, err)
		}
		return nil
	}

	// File exists; ensure it is a regular file and is writable.
	if info.IsDir() {
		return fmt.Errorf("%s: %s is a directory, not a file", key, path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("%s: %s is not writable: %w", key, path, err)
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
