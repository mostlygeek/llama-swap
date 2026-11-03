package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// restorer implements hooks.on_unload.restore: after a model stops (unload,
// ttl, eviction, crash) and no other model has stopped for the configured
// delay, every restore model that is not running is loaded again, but only if
// loading it evicts nothing. Resuming paused inference schedules a pass too.
type restorer struct {
	delay  time.Duration
	run    func()
	cancel []context.CancelFunc

	mu      sync.Mutex
	timer   *time.Timer
	stopped bool
	// busy serialises passes: a pass that starts while another is loading
	// models waits for it instead of racing it.
	busy sync.Mutex
}

func (s *Server) startRestore() {
	hook := s.cfg.Hooks.OnUnload
	if len(hook.Restore) == 0 {
		return
	}
	r := &restorer{delay: time.Duration(hook.Delay) * time.Second}
	r.run = func() { s.restorePass(r) }
	r.cancel = []context.CancelFunc{
		event.On(func(e swaputil.ProcessStateChangeEvent) {
			// A failed start goes starting -> stopped; ignoring it keeps a
			// model that cannot load from being retried forever.
			if e.NewState == string(process.StateStopped) && e.OldState != string(process.StateStarting) {
				r.schedule()
			}
		}),
		event.On(func(e swaputil.InferencePausedEvent) {
			if !e.Paused {
				r.schedule()
			}
		}),
	}
	s.restore = r
}

// schedule (re)starts the quiet-period timer, so a burst of stops (a swap
// evicting several models, an unload of everything) leads to one pass.
func (r *restorer) schedule() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timer = time.AfterFunc(r.delay, r.run)
}

// stop unsubscribes and cancels a pending pass. Safe on a nil restorer.
func (r *restorer) stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.stopped = true
	if r.timer != nil {
		r.timer.Stop()
	}
	r.mu.Unlock()
	for _, c := range r.cancel {
		c()
	}
}

func (s *Server) restorePass(r *restorer) {
	r.busy.Lock()
	defer r.busy.Unlock()

	for _, modelID := range s.cfg.Hooks.OnUnload.Restore {
		if s.shutdownCtx.Err() != nil {
			return
		}
		if !s.local.Handles(modelID) {
			s.logs.ProxyLogs.Warnf("restore: model %s is not a local model, skipping", modelID)
			continue
		}
		if s.pause.Paused() {
			s.logs.ProxyLogs.Debugf("restore: inference is paused, skipping")
			return
		}
		if _, running := s.local.RunningModels()[modelID]; running {
			continue
		}

		status := s.warmModel(modelID, true)
		if status == http.StatusConflict {
			s.logs.ProxyLogs.Debugf("restore: model %s not loaded, it would evict another model", modelID)
			continue
		}
		// warmModel's status includes the upstream's answer to GET /, which
		// some servers do not route, so judge by the process state instead.
		if s.local.RunningModels()[modelID] == process.StateReady {
			s.logs.ProxyLogs.Infof("restore: model %s loaded", modelID)
		} else {
			s.logs.ProxyLogs.Errorf("restore: failed to load model %s: status %d", modelID, status)
		}
	}
}
