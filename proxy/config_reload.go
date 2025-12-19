package proxy

import (
	"log"
	"slices"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

// modelNeedsRestart returns true if the model config changed in a way that requires restart
func modelNeedsRestart(old, new config.ModelConfig) bool {
	// These fields require restart if changed
	if old.Cmd != new.Cmd {
		return true
	}
	if old.CmdStop != new.CmdStop {
		return true
	}
	if old.Proxy != new.Proxy {
		return true
	}
	if old.CheckEndpoint != new.CheckEndpoint {
		return true
	}
	if !slices.Equal(old.Env, new.Env) {
		return true
	}
	if old.ConcurrencyLimit != new.ConcurrencyLimit {
		return true
	}
	return false
}

// shouldRestartModel returns true if the model should be restarted based on config changes and restart settings
func shouldRestartModel(old, new config.ModelConfig, globalRestart bool) bool {
	if !modelNeedsRestart(old, new) {
		return false
	}

	// Per-model setting overrides global
	if new.ForceRestart != nil {
		return *new.ForceRestart
	}

	return globalRestart
}

// configWatcher monitors a configuration file for changes and triggers
// a debounced reload callback when modifications are detected.
type configWatcher struct {
	mu        sync.Mutex
	watcher   *fsnotify.Watcher
	debouncer *debouncer
	stopChan  chan struct{}
	stopped   bool
}

// newConfigWatcher creates a file watcher that monitors path for changes.
// When changes are detected, onReload is called after debounceDelay has elapsed
// with no additional changes. This handles editors that perform atomic writes
// (write to temp file, then rename).
func newConfigWatcher(path string, debounceDelay time.Duration, onReload func(path string)) (*configWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Add path to watcher before starting goroutine for cleaner error handling
	if err := watcher.Add(path); err != nil {
		watcher.Close()
		return nil, err
	}

	cw := &configWatcher{
		watcher:  watcher,
		stopChan: make(chan struct{}),
	}

	cw.debouncer = newDebouncer(debounceDelay, func() {
		onReload(path)
	})

	// Start watching after setup is complete
	go cw.watchLoop()

	return cw, nil
}

// watchLoop continuously monitors for file system events until stop() is called.
func (cw *configWatcher) watchLoop() {
	for {
		select {
		case <-cw.stopChan:
			return
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			// Handle Write, Create, and Rename for editors that use atomic writes
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
				cw.debouncer.trigger()
			}
		case err, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Config watcher error: %v", err)
		}
	}
}

// stop terminates the file watcher and cancels any pending reload.
func (cw *configWatcher) stop() {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	if cw.stopped {
		return
	}
	cw.stopped = true
	close(cw.stopChan)
	cw.debouncer.stop()
	cw.watcher.Close()
}
