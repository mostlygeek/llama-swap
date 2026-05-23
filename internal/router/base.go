package router

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

type shutdownReq struct {
	timeout time.Duration
	respond chan error
}

type handlerReq struct {
	model   string
	ctx     context.Context
	respond chan handlerResp
}

type handlerResp struct {
	handleFunc http.HandlerFunc
	err        error
}

type swapDone struct {
	modelID string
	err     error
}

type activeSwap struct {
	modelID string
	evict   []string
	waiters []handlerReq
}

// swapPlanner is the only piece of behaviour that differs between concrete
// routers. baseRouter never inspects its internals.
type swapPlanner interface {
	// EvictionFor returns running model IDs that must be stopped before
	// target can serve. alsoRunning lists models the baseRouter has already
	// committed to loading (in-flight swaps) which the planner cannot see
	// via process.State() yet. Pure decision; must not log.
	EvictionFor(target string, alsoRunning []string) []string

	// OnSwapStart runs once at the start of every swap. Planners may log
	// their decision here at whatever verbosity they choose.
	OnSwapStart(target string)
}

// baseRouter owns the channels, run-loop, and orchestration code shared by
// every concrete router. Concrete routers embed *baseRouter and supply a
// swapPlanner that captures how their eviction set is decided.
type baseRouter struct {
	name      string
	config    config.Config
	processes map[string]process.Process
	logger    *logmon.Monitor
	planner   swapPlanner

	shutdownCtx  context.Context
	shutdownFn   context.CancelFunc
	shuttingDown atomic.Bool

	handlerCh  chan handlerReq
	shutdownCh chan shutdownReq
	swapDoneCh chan swapDone

	runDone chan struct{}

	// testProcessed, when non-nil, receives one event after each handlerReq
	// or swapDone has been fully processed by run(). Tests use it to wait
	// for run() to reach a deterministic state without sleeping.
	testProcessed chan struct{}
}

func newBaseRouter(name string, conf config.Config, processes map[string]process.Process, planner swapPlanner, logger *logmon.Monitor) *baseRouter {
	shutdownCtx, shutdownFn := context.WithCancel(context.Background())
	return &baseRouter{
		name:        name,
		config:      conf,
		processes:   processes,
		logger:      logger,
		planner:     planner,
		shutdownCtx: shutdownCtx,
		shutdownFn:  shutdownFn,
		handlerCh:   make(chan handlerReq),
		shutdownCh:  make(chan shutdownReq),
		swapDoneCh:  make(chan swapDone),
		runDone:     make(chan struct{}),
	}
}

func (b *baseRouter) notifyProcessed() {
	if b.testProcessed != nil {
		b.testProcessed <- struct{}{}
	}
}

func (b *baseRouter) run() {
	defer close(b.runDone)

	active := make(map[string]*activeSwap)
	var queued []handlerReq

	for {
		select {
		case req := <-b.shutdownCh:
			b.handleShutdown(req, active, queued)
			return

		case req := <-b.handlerCh:
			b.handleRequest(req, active, &queued)
			b.notifyProcessed()

		case ev := <-b.swapDoneCh:
			b.handleSwapDone(ev, active, &queued)
			b.notifyProcessed()
		}
	}
}

// handleRequest decides what to do with one incoming ServeHTTP request. It is
// called from run() and never blocks: any work that has to wait (starting a
// process, stopping siblings, waiting for ready) is deferred to a swap
// goroutine and reported back via swapDoneCh.
//
// The decision tree, in order:
//
//  1. Unknown model — respond with ErrNoLocalModelFound and move on.
//  2. A swap to the same model is already in flight — attach this waiter so
//     one swap serves all callers that asked for the same model.
//  3. Fast path — the target process is already ready, the planner sees
//     nothing to evict, and no in-flight swap is evicting it. Hand back its
//     ServeHTTP immediately.
//  4. Would collide with an in-flight swap (we'd stop their target, or they're
//     stopping us) — park in the queue for handleSwapDone to drain.
//  5. Otherwise — start a new swap. This may run in parallel with other
//     active swaps when their evict sets don't intersect.
func (b *baseRouter) handleRequest(req handlerReq, active map[string]*activeSwap, queued *[]handlerReq) {
	// (1) Unknown model.
	p, ok := b.processes[req.model]
	if !ok {
		req.respond <- handlerResp{err: ErrNoLocalModelFound}
		return
	}

	// (2) Join an in-flight swap for the same model.
	if s, ok := active[req.model]; ok {
		s.waiters = append(s.waiters, req)
		return
	}

	evict := b.planner.EvictionFor(req.model, activeTargets(active, req.model))

	// (3) Fast path: ready, nothing to evict, and nobody is evicting us.
	if p.State() == process.StateReady && len(evict) == 0 && !collidesWith(req.model, evict, active) {
		req.respond <- handlerResp{handleFunc: p.ServeHTTP}
		return
	}

	// (4) Collision with an in-flight swap — queue.
	if collidesWith(req.model, evict, active) {
		*queued = append(*queued, req)
		return
	}

	// (5) Start a new (possibly parallel) swap.
	s := b.startSwap(req, evict)
	active[s.modelID] = s
}

// handleSwapDone is called from run() when a swap goroutine reports that it
// has finished. It fans out the result to every waiter that joined this swap,
// removes the swap from the active map, and then walks the queue once,
// promoting any items that no longer collide with the remaining active set.
// FIFO order is preserved: items still blocked stay in place.
func (b *baseRouter) handleSwapDone(ev swapDone, active map[string]*activeSwap, queued *[]handlerReq) {
	s, ok := active[ev.modelID]
	if !ok {
		return
	}
	delete(active, ev.modelID)

	for _, w := range s.waiters {
		if ev.err != nil {
			w.respond <- handlerResp{err: ev.err}
		} else {
			p := b.processes[ev.modelID]
			w.respond <- handlerResp{handleFunc: p.ServeHTTP}
		}
	}

	b.drainQueue(active, queued)
}

// drainQueue walks the queued requests in order, re-running the handleRequest
// decision tree against the (now smaller) active set. Items that can now start
// or join become satisfied; items still blocked remain queued in original
// order so they get another chance on the next swap completion.
func (b *baseRouter) drainQueue(active map[string]*activeSwap, queued *[]handlerReq) {
	if len(*queued) == 0 {
		return
	}
	pending := *queued
	var remaining []handlerReq
	for _, req := range pending {
		p, ok := b.processes[req.model]
		if !ok {
			req.respond <- handlerResp{err: ErrNoLocalModelFound}
			continue
		}
		if s, ok := active[req.model]; ok {
			s.waiters = append(s.waiters, req)
			continue
		}
		evict := b.planner.EvictionFor(req.model, activeTargets(active, req.model))
		if p.State() == process.StateReady && len(evict) == 0 && !collidesWith(req.model, evict, active) {
			req.respond <- handlerResp{handleFunc: p.ServeHTTP}
			continue
		}
		if collidesWith(req.model, evict, active) {
			remaining = append(remaining, req)
			continue
		}
		s := b.startSwap(req, evict)
		active[s.modelID] = s
	}
	*queued = remaining
}

func (b *baseRouter) startSwap(initial handlerReq, evict []string) *activeSwap {
	swap := &activeSwap{
		modelID: initial.model,
		evict:   evict,
		waiters: []handlerReq{initial},
	}
	b.planner.OnSwapStart(initial.model)
	go b.doSwap(initial.model, evict)
	return swap
}

// activeTargets returns the IDs of every in-flight swap target except exclude.
// baseRouter passes this to the planner so eviction decisions account for
// models that have been committed to but have not yet transitioned to
// StateStarting in their process state machine.
func activeTargets(active map[string]*activeSwap, exclude string) []string {
	if len(active) == 0 {
		return nil
	}
	out := make([]string, 0, len(active))
	for id := range active {
		if id == exclude {
			continue
		}
		out = append(out, id)
	}
	return out
}

// collidesWith reports whether a new swap with this target and evict set can
// safely run alongside the currently active swaps. Same-target callers should
// JOIN (handled before this) — they do not collide with themselves.
func collidesWith(target string, evict []string, active map[string]*activeSwap) bool {
	for id, s := range active {
		if id == target {
			continue
		}
		if containsString(evict, id) {
			return true
		}
		if containsString(s.evict, target) {
			return true
		}
	}
	return false
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func (b *baseRouter) doSwap(modelID string, toStop []string) {
	timeout := b.healthCheckTimeout()

	var wg sync.WaitGroup
	for _, mID := range toStop {
		wg.Add(1)
		go func(p process.Process, id string) {
			defer wg.Done()
			if err := p.Stop(timeout); err != nil {
				b.logger.Warnf("%s: stopping %s failed: %v", b.name, id, err)
			}
		}(b.processes[mID], mID)
	}
	wg.Wait()

	target := b.processes[modelID]
	if target.State() == process.StateStopped {
		go func() {
			if err := target.Run(timeout); err != nil {
				b.logger.Warnf("%s: running %s exited: %v", b.name, modelID, err)
			}
		}()
	}

	err := target.WaitReady(b.shutdownCtx)

	select {
	case b.swapDoneCh <- swapDone{modelID: modelID, err: err}:
	case <-b.shutdownCtx.Done():
	}
}

func (b *baseRouter) handleShutdown(req shutdownReq, active map[string]*activeSwap, queued []handlerReq) {
	shutdownErr := fmt.Errorf("%s is shutting down", b.name)
	for _, s := range active {
		for _, w := range s.waiters {
			w.respond <- handlerResp{err: shutdownErr}
		}
	}
	for _, w := range queued {
		w.respond <- handlerResp{err: shutdownErr}
	}

	stopTimeout := req.timeout
	if stopTimeout <= 0 {
		stopTimeout = b.healthCheckTimeout()
	}

	var wg sync.WaitGroup
	for i, p := range b.processes {
		wg.Add(1)
		go func(id string, p process.Process) {
			defer wg.Done()
			if err := p.Stop(stopTimeout); err != nil {
				b.logger.Warnf("%s failed to stop process %s: %v", b.name, id, err)
			}
		}(i, p)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	if req.timeout > 0 {
		select {
		case <-done:
		case <-time.After(req.timeout):
			b.shutdownFn()
			<-done
		}
	} else {
		<-done
	}

	b.shutdownFn()
	req.respond <- nil
}

func (b *baseRouter) healthCheckTimeout() time.Duration {
	t := time.Duration(b.config.HealthCheckTimeout) * time.Second
	if t <= 0 {
		return 30 * time.Second
	}
	return t
}

func (b *baseRouter) Handles(model string) bool {
	_, ok := b.processes[model]
	return ok
}

func (b *baseRouter) ProcessLogger(modelID string) (*logmon.Monitor, bool) {
	if p, ok := b.processes[modelID]; ok {
		return p.Logger(), true
	}
	return nil, false
}

// RunningModels returns the current state of every process that is not stopped
// or shut down. The processes map keys are fixed at construction and State()
// is a snapshot, so this is safe to call without the run loop.
func (b *baseRouter) RunningModels() map[string]process.ProcessState {
	running := make(map[string]process.ProcessState)
	for id, p := range b.processes {
		st := p.State()
		if st == process.StateStopped || st == process.StateShutdown {
			continue
		}
		running[id] = st
	}
	return running
}

// Unload stops the named models, or every running model when none are named.
// It blocks until each targeted process has stopped. An in-flight swap whose
// target is stopped resolves to an error for its waiters, who may retry.
func (b *baseRouter) Unload(timeout time.Duration, models ...string) {
	targets := models
	if len(targets) == 0 {
		targets = make([]string, 0, len(b.processes))
		for id := range b.processes {
			targets = append(targets, id)
		}
	}

	var wg sync.WaitGroup
	for _, id := range targets {
		p, ok := b.processes[id]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(id string, p process.Process) {
			defer wg.Done()
			if err := p.Stop(timeout); err != nil {
				b.logger.Warnf("%s: unloading %s failed: %v", b.name, id, err)
			}
		}(id, p)
	}
	wg.Wait()
}

func (b *baseRouter) Shutdown(timeout time.Duration) error {
	if !b.shuttingDown.CompareAndSwap(false, true) {
		return fmt.Errorf("%s shutdown already in progress", b.name)
	}
	req := shutdownReq{timeout: timeout, respond: make(chan error, 1)}
	select {
	case b.shutdownCh <- req:
	case <-b.runDone:
		return nil
	}
	return <-req.respond
}

func (b *baseRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if b.shuttingDown.Load() {
		SendError(w, req, fmt.Errorf("%s is shutting down", b.name))
		return
	}

	_, realModel, err := FetchModel(req, b.config)
	if err != nil {
		SendError(w, req, err)
		return
	}

	hr := handlerReq{
		model:   realModel,
		ctx:     req.Context(),
		respond: make(chan handlerResp, 1),
	}

	select {
	case b.handlerCh <- hr:
	case <-req.Context().Done():
		return
	case <-b.shutdownCtx.Done():
		SendError(w, req, fmt.Errorf("%s is shutting down", b.name))
		return
	}

	select {
	case resp := <-hr.respond:
		if resp.err != nil {
			SendError(w, req, resp.err)
			return
		}
		resp.handleFunc(w, req)
	case <-req.Context().Done():
		return
	case <-b.shutdownCtx.Done():
		SendError(w, req, fmt.Errorf("%s is shutting down", b.name))
		return
	}
}
