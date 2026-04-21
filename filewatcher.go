package main

import (
	"os"
	"time"
)

// FileWatcher polls files for changes using fstat (modification time comparison).
// It is cross-platform (POSIX and Windows), supports symlinks, and works
// in Docker containers with volume-mounted files.
type FileWatcher struct {
	interval time.Duration
	paths    map[string]time.Time // path -> last known mtime
	done     chan struct{}
	onChange func()
}

// NewFileWatcher creates a new FileWatcher that polls at the given interval.
// Call Start() to begin watching.
func NewFileWatcher(interval time.Duration) *FileWatcher {
	return &FileWatcher{
		interval: interval,
		paths:    make(map[string]time.Time),
		done:     make(chan struct{}),
	}
}

// Add registers a file path to watch. The path can be a regular file or a
// symlink; os.Stat is used so symlinks are followed (important for k8s configmap
// ..data swap pattern).
func (fw *FileWatcher) Add(path string) {
	fw.paths[path] = time.Time{}
}

// Start begins the polling loop in a goroutine. The onChange callback is
// invoked whenever any watched file's modification time changes.
func (fw *FileWatcher) Start(onChange func()) {
	fw.onChange = onChange

	go func() {
		ticker := time.NewTicker(fw.interval)
		defer ticker.Stop()

		for {
			select {
			case <-fw.done:
				return
			case <-ticker.C:
				for path, lastMtime := range fw.paths {
					info, err := os.Stat(path)
					if err != nil {
						// File may have been deleted or is temporarily unavailable.
						// Reset the tracked mtime so re-creation is detected.
						fw.paths[path] = time.Time{}
						continue
					}

					mtime := info.ModTime()
					if !mtime.Equal(lastMtime) {
						fw.paths[path] = mtime
						if fw.onChange != nil {
							fw.onChange()
						}
						break
					}
				}
			}
		}
	}()
}

// Stop signals the polling loop to exit.
func (fw *FileWatcher) Stop() {
	close(fw.done)
}
