package proxy

import (
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

type configWatcher struct {
	mu        sync.Mutex
	watcher   *fsnotify.Watcher
	debouncer *debouncer
	stopChan  chan struct{}
	stopped   bool
}

func newConfigWatcher(path string, debounceDelay time.Duration, onReload func(path string)) (*configWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	cw := &configWatcher{
		watcher:  watcher,
		stopChan: make(chan struct{}),
	}

	cw.debouncer = newDebouncer(debounceDelay, func() {
		onReload(path)
	})

	// Start watching
	go cw.watchLoop()

	if err := watcher.Add(path); err != nil {
		close(cw.stopChan) // Signal watchLoop to stop
		cw.debouncer.stop()
		watcher.Close()
		return nil, err
	}

	return cw, nil
}

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
		case _, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
			// Log error but continue watching
		}
	}
}

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
