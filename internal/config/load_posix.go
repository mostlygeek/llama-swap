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
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("store.path must not be empty")
	}

	if info, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("store.path: %w", err)
		}
		// File does not exist; ensure the parent directory is writable.
		dir := filepath.Dir(path)
		if err := unix.Access(dir, unix.W_OK); err != nil {
			return fmt.Errorf("store.path: directory %s is not writable: %w", dir, err)
		}
		return nil
	} else if info.IsDir() {
		return fmt.Errorf("store.path: %s is a directory, not a file", path)
	}

	// File exists; ensure it is writable.
	if err := unix.Access(path, unix.W_OK); err != nil {
		return fmt.Errorf("store.path: %s is not writable: %w", path, err)
	}
	return nil
}
