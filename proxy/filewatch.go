package proxy

import (
	"os"
	"sync"
	"time"
)

// FileWatcher polls a filesystem path for changes using os.Stat.
// It works on POSIX and Windows, follows symlinks, and is safe to use
// inside Docker containers where inotify/fsnotify may not work for
// volume-mounted files.
type FileWatcher struct {
	path      string
	interval  time.Duration
	changed   chan struct{}
	stop      chan struct{}
	wg        sync.WaitGroup
	lastMod   time.Time
	lastSize  int64
	errLogged bool
}

// NewFileWatcher creates a new FileWatcher for the given path.
func NewFileWatcher(path string, interval time.Duration) *FileWatcher {
	return &FileWatcher{
		path:     path,
		interval: interval,
		changed:  make(chan struct{}, 1),
		stop:     make(chan struct{}),
	}
}

// Start begins polling. The returned channel receives a notification
// whenever the file's modification time or size changes. It follows
// symlinks automatically because os.Stat resolves them.
func (fw *FileWatcher) Start() <-chan struct{} {
	fw.wg.Add(1)
	go fw.poll()
	return fw.changed
}

// Stop halts the polling goroutine.
func (fw *FileWatcher) Stop() {
	close(fw.stop)
	fw.wg.Wait()
}

func (fw *FileWatcher) poll() {
	defer fw.wg.Done()

	// Initial stat to establish baseline.
	fw.stat()

	ticker := time.NewTicker(fw.interval)
	defer ticker.Stop()

	for {
		select {
		case <-fw.stop:
			return
		case <-ticker.C:
			if fw.hasChanged() {
				select {
				case fw.changed <- struct{}{}:
				default:
					// Channel is buffered; drop if full to avoid blocking.
				}
			}
		}
	}
}

func (fw *FileWatcher) stat() {
	info, err := os.Stat(fw.path)
	if err != nil {
		if !fw.errLogged {
			// Only log the error once to avoid spam.
			fw.errLogged = true
		}
		// Treat missing file as a change so the consumer can react.
		return
	}
	fw.errLogged = false
	fw.lastMod = info.ModTime()
	fw.lastSize = info.Size()
}

func (fw *FileWatcher) hasChanged() bool {
	info, err := os.Stat(fw.path)
	if err != nil {
		wasMissing := fw.lastMod.IsZero() && fw.lastSize == 0
		fw.lastMod = time.Time{}
		fw.lastSize = 0
		return !wasMissing
	}

	changed := info.ModTime() != fw.lastMod || info.Size() != fw.lastSize
	fw.lastMod = info.ModTime()
	fw.lastSize = info.Size()
	return changed
}
