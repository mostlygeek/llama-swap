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
	delay time.Duration
	// warmTimeout bounds how long a pass waits on one model's warm-up
	// request, so an upstream that is healthy but never answers GET / cannot
	// hold the pass (and every later one) forever.
	warmTimeout time.Duration
	run         func(gen uint64)
	cancel      []context.CancelFunc

	mu      sync.Mutex
	timer   *time.Timer
	gen     uint64 // bumped by every schedule; a pass for an older gen is stale
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
	// A warm-up covers the whole load, which healthCheckTimeout already
	// bounds, plus the upstream answering GET /.
	hc := s.cfg.HealthCheckTimeout
	if hc <= 0 {
		hc = 120
	}
	r := &restorer{
		delay:       time.Duration(hook.Delay) * time.Second,
		warmTimeout: time.Duration(hc)*time.Second + 30*time.Second,
	}
	r.run = func(gen uint64) { s.restorePass(r, gen) }
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
// evicting several models, an unload of everything) leads to one pass. Each
// call starts a new generation; a pass from an earlier one that is already
// waiting for busy finds itself stale and does nothing.
func (r *restorer) schedule() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	r.gen++
	gen := r.gen
	r.timer = time.AfterFunc(r.delay, func() { r.run(gen) })
}

// current reports whether gen is still the latest scheduled pass.
func (r *restorer) current(gen uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.stopped && gen == r.gen
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

func (s *Server) restorePass(r *restorer, gen uint64) {
	r.busy.Lock()
	defer r.busy.Unlock()

	for _, modelID := range s.cfg.Hooks.OnUnload.Restore {
		// A stop that happened while this pass waited or ran restarted the
		// quiet period; leave the rest to that newer pass.
		if !r.current(gen) || s.shutdownCtx.Err() != nil {
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

		ctx, cancel := context.WithTimeout(s.shutdownCtx, r.warmTimeout)
		status := s.warmModel(ctx, modelID, true)
		timedOut := ctx.Err() == context.DeadlineExceeded
		cancel()
		if status == http.StatusConflict {
			s.logs.ProxyLogs.Debugf("restore: model %s not loaded, it would evict another model", modelID)
			continue
		}
		// warmModel's status includes the upstream's answer to GET /, which
		// some servers do not route, so judge by the process state instead.
		switch state := s.local.RunningModels()[modelID]; {
		case state == process.StateReady:
			s.logs.ProxyLogs.Infof("restore: model %s loaded", modelID)
		case timedOut:
			s.logs.ProxyLogs.Warnf("restore: gave up waiting for model %s after %s (state %q)", modelID, r.warmTimeout, state)
		default:
			s.logs.ProxyLogs.Errorf("restore: failed to load model %s: status %d", modelID, status)
		}
	}
}
