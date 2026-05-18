package router

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/proxy/config"
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
	waiters []handlerReq
}

// swapPlanner is the only piece of behaviour that differs between concrete
// routers. baseRouter never inspects its internals.
type swapPlanner interface {
	// EvictionFor returns running model IDs that must be stopped before
	// target can serve. Pure decision; must not log.
	EvictionFor(target string) []string

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

	var active *activeSwap
	var queued []handlerReq

	for {
		select {
		case req := <-b.shutdownCh:
			b.handleShutdown(req, active, queued)
			return

		case req := <-b.handlerCh:
			active = b.handleRequest(req, active, &queued)
			b.notifyProcessed()

		case ev := <-b.swapDoneCh:
			active = b.handleSwapDone(ev, active, &queued)
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
//  2. Fast path — the target process is already ready and the planner says
//     nothing else needs to be stopped. Hand back its ServeHTTP immediately.
//  3. A swap to the same model is already in flight — attach this waiter to
//     it so one swap serves all callers that asked for the same model.
//  4. A swap to a different model is in flight — park this request in the
//     queue. handleSwapDone() will promote it once the current swap finishes.
//  5. Nothing in flight — kick off a new swap for this model.
func (b *baseRouter) handleRequest(req handlerReq, active *activeSwap, queued *[]handlerReq) *activeSwap {
	// (1) Unknown model.
	p, ok := b.processes[req.model]
	if !ok {
		req.respond <- handlerResp{err: ErrNoLocalModelFound}
		return active
	}

	// (2) Fast path: ready and the planner sees nothing to evict.
	if active == nil && p.State() == process.StateReady && len(b.planner.EvictionFor(req.model)) == 0 {
		req.respond <- handlerResp{handleFunc: p.ServeHTTP}
		return active
	}

	// (3) Join an in-flight swap for the same model.
	if active != nil && active.modelID == req.model {
		active.waiters = append(active.waiters, req)
		return active
	}

	// (4) Different model is being swapped in — queue for the next round.
	if active != nil {
		*queued = append(*queued, req)
		return active
	}

	// (5) Nothing in flight — start a new swap for this request.
	return b.startSwap(req)
}

// handleSwapDone is called from run() when a swap goroutine reports that it
// has finished (target is ready, or it failed/was cancelled). It does two
// things:
//
//  1. Fan out the result to every waiter that joined this swap. On success
//     they get the target's ServeHTTP; on failure they get the error.
//  2. Promote one queued request — if any — into a new swap. The first item
//     in the queue chooses the next target; any other queued requests for
//     that same model are pulled in as additional waiters so they all ride
//     the same swap. Requests for other models stay queued for later rounds.
//
// Returns the new active swap (or nil if the queue was empty).
func (b *baseRouter) handleSwapDone(ev swapDone, active *activeSwap, queued *[]handlerReq) *activeSwap {
	if active == nil {
		return nil
	}

	// (1) Notify everyone who was waiting on this swap.
	for _, w := range active.waiters {
		if ev.err != nil {
			w.respond <- handlerResp{err: ev.err}
		} else {
			p := b.processes[ev.modelID]
			w.respond <- handlerResp{handleFunc: p.ServeHTTP}
		}
	}

	// Nothing queued — go back to idle.
	if len(*queued) == 0 {
		return nil
	}

	// (2) Promote the head of the queue into a new swap.
	next := (*queued)[0]
	remaining := (*queued)[1:]
	newSwap := b.startSwap(next)

	// Pull in any other queued requests asking for the same model; leave
	// requests for other models in the queue for the next swapDone.
	var leftover []handlerReq
	for _, r := range remaining {
		if r.model == newSwap.modelID {
			newSwap.waiters = append(newSwap.waiters, r)
		} else {
			leftover = append(leftover, r)
		}
	}
	*queued = leftover
	return newSwap
}

func (b *baseRouter) startSwap(initial handlerReq) *activeSwap {
	swap := &activeSwap{
		modelID: initial.model,
		waiters: []handlerReq{initial},
	}
	toStop := b.planner.EvictionFor(initial.model)
	b.planner.OnSwapStart(initial.model)
	go b.doSwap(initial.model, toStop)
	return swap
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

func (b *baseRouter) handleShutdown(req shutdownReq, active *activeSwap, queued []handlerReq) {
	shutdownErr := fmt.Errorf("%s router is shutting down", b.name)
	if active != nil {
		for _, w := range active.waiters {
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
	for _, p := range b.processes {
		wg.Add(1)
		go func(p process.Process) {
			defer wg.Done()
			_ = p.Stop(stopTimeout)
		}(p)
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

func (b *baseRouter) Shutdown(timeout time.Duration) error {
	if !b.shuttingDown.CompareAndSwap(false, true) {
		return fmt.Errorf("shutdown already in progress")
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
		SendError(w, req, fmt.Errorf("%s router is shutting down", b.name))
		return
	}

	model, err := FetchModel(req)
	if err != nil {
		SendError(w, req, err)
		return
	}

	hr := handlerReq{
		model:   model,
		ctx:     req.Context(),
		respond: make(chan handlerResp, 1),
	}

	select {
	case b.handlerCh <- hr:
	case <-req.Context().Done():
		return
	case <-b.shutdownCtx.Done():
		SendError(w, req, fmt.Errorf("%s router is shutting down", b.name))
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
		SendError(w, req, fmt.Errorf("%s router is shutting down", b.name))
		return
	}
}
