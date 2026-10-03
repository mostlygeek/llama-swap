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

// validateDatabasePath checks a SQLite file path for the store section. key is
// the config key being validated, so the error names the setting the user has
// to edit.
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
		// Windows because it only reflects the read-only attribute.
		dir := filepath.Dir(path)
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

// sameStorePath reports whether two store settings point at the same file.
// Windows paths are case-insensitive, so the comparison is too.
func sameStorePath(a string, b string) bool {
	return strings.EqualFold(cleanStorePath(a), cleanStorePath(b))
}

func cleanStorePath(path string) string {
	abs, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return strings.TrimSpace(path)
	}
	return abs
}

func checkDirWritableWindows(dir string) error {
	tmp, err := os.CreateTemp(dir, ".llama-swap-write-test-*")
	if err != nil {
		return err
	}
	tmp.Close()
	return os.Remove(tmp.Name())
}
