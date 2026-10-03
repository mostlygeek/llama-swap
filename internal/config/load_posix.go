//go:build !windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
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

	if info, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("%s: %w", key, err)
		}
		// File does not exist; ensure the parent directory is writable.
		dir := filepath.Dir(path)
		if err := unix.Access(dir, unix.W_OK); err != nil {
			return fmt.Errorf("%s: directory %s is not writable: %w", key, dir, err)
		}
		return nil
	} else if info.IsDir() {
		return fmt.Errorf("%s: %s is a directory, not a file", key, path)
	}

	// File exists; ensure it is writable.
	if err := unix.Access(path, unix.W_OK); err != nil {
		return fmt.Errorf("%s: %s is not writable: %w", key, path, err)
	}
	return nil
}

// sameStorePath reports whether two store settings point at the same file.
func sameStorePath(a string, b string) bool {
	return cleanStorePath(a) == cleanStorePath(b)
}

func cleanStorePath(path string) string {
	abs, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return strings.TrimSpace(path)
	}
	return abs
}
