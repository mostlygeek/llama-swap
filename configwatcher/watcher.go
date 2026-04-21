package configwatcher

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Watcher struct {
	path        string
	interval    time.Duration
	onChange    func()
	done        chan struct{}
	wg          sync.WaitGroup
	lastModTime time.Time
	lastSize    int64
}

func NewWatcher(path string, interval time.Duration, onChange func()) *Watcher {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	return &Watcher{
		path:     absPath,
		interval: interval,
		onChange: onChange,
		done:     make(chan struct{}),
	}
}

func (w *Watcher) Start() {
	info, err := os.Stat(w.path)
	if err == nil {
		w.lastModTime = info.ModTime()
		w.lastSize = info.Size()
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				w.check()
			case <-w.done:
				return
			}
		}
	}()
}

func (w *Watcher) Stop() {
	close(w.done)
	w.wg.Wait()
}

func (w *Watcher) check() {
	info, err := os.Stat(w.path)
	if err != nil {
		return
	}

	if info.ModTime() != w.lastModTime || info.Size() != w.lastSize {
		w.lastModTime = info.ModTime()
		w.lastSize = info.Size()
		w.onChange()
	}
}
