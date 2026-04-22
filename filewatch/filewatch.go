// Package filewatch provides a simple file watcher using fstat-based polling.
// It works with Docker volume mounts, symlinks, and is compatible with POSIX
// and Windows systems.
package filewatch

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Watcher polls a file for changes using stat-based comparison.
type Watcher struct {
	path     string
	interval time.Duration
	done     chan struct{}
	mu       sync.Mutex
	stopped  bool
}

// NewWatcher creates a new file watcher that polls at the given interval.
func NewWatcher(path string, interval time.Duration) *Watcher {
	return &Watcher{
		path:     path,
		interval: interval,
		done:     make(chan struct{}),
	}
}

// Start begins polling the file and returns a channel that receives a struct{}
// whenever the file changes. The channel is non-blocking; callers should
// read from it promptly to avoid missing events.
func (w *Watcher) Start() <-chan struct{} {
	changes := make(chan struct{}, 1)

	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return changes
	}
	w.mu.Unlock()

	go w.watch(changes)
	return changes
}

// Close stops the watcher.
func (w *Watcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stopped {
		w.stopped = true
		close(w.done)
	}
}

func (w *Watcher) watch(changes chan<- struct{}) {
	var lastMod time.Time
	var lastModValid bool

	for {
		select {
		case <-w.done:
			return
		case <-time.After(w.interval):
		}

		statPath := w.path

		// Resolve symlinks to get the actual file for stat
		resolved, err := filepath.EvalSymlinks(w.path)
		if err == nil {
			statPath = resolved
		}

		info, err := os.Stat(statPath)
		if err != nil {
			// File doesn't exist yet or can't be stat'd; just skip this poll
			continue
		}

		modTime := info.ModTime()

		w.mu.Lock()
		if w.stopped {
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()

		if lastModValid && !modTime.Equal(lastMod) {
			select {
			case changes <- struct{}{}:
			default:
			}
			lastMod = modTime
			lastModValid = true
		} else if !lastModValid {
			lastMod = modTime
			lastModValid = true
		}
	}
}
