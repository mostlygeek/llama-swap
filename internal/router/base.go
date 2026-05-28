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

type unloadReq struct {
	targets []string
	timeout time.Duration
	respond chan struct{}
}

type handlerReq struct {
	model      string
	ctx        context.Context
	respond    chan handlerResp
	positionCh chan int
}

type handlerResp struct {
	handleFunc http.HandlerFunc
	err        error
}

type swapDone struct {
	modelID string
	err     error
}

type serveDoneEvent struct {
	modelID string
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

	handlerCh   chan handlerReq
	shutdownCh  chan shutdownReq
	unloadCh    chan unloadReq
	swapDoneCh  chan swapDone
	serveDoneCh chan serveDoneEvent

	runDone chan struct{}

	// testProcessed, when non-nil, receives one event after each handlerReq
	// or swapDone has been fully processed by run(). Tests use it to wait
	// for run() to reach a deterministic state without sleeping. serveDone
	// events are intentionally NOT signalled here so test event counts
	// remain stable.
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
		unloadCh:    make(chan unloadReq),
		swapDoneCh:  make(chan swapDone),
		serveDoneCh: make(chan serveDoneEvent),
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
	inFlight := make(map[string]int)
	var queued []handlerReq

	for {
		select {
		case req := <-b.shutdownCh:
			b.handleShutdown(req, active, queued)
			return

		case req := <-b.handlerCh:
			b.handleRequest(req, active, inFlight, &queued)
			b.notifyProcessed()

		case req := <-b.unloadCh:
			b.handleUnload(req, active, inFlight, &queued)
			b.notifyProcessed()

		case ev := <-b.swapDoneCh:
			b.handleSwapDone(ev, active, inFlight, &queued)
			b.notifyProcessed()

		case ev := <-b.serveDoneCh:
			b.handleServeDone(ev, active, inFlight, &queued)
		}
	}
}

// grant sends a response back to the caller of ServeHTTP and tells us
// whether the caller was still there to receive it.
//
// Each ServeHTTP creates a fresh, UNBUFFERED respond channel and parks in
// a select waiting on it. "Unbuffered" is the important word: a send only
// completes when the other side is actively receiving. So if this send
// succeeds, we know for a fact the caller picked up the response and will
// act on it. If the caller has already given up (its request context was
// cancelled, e.g. the HTTP client disconnected) or the router is shutting
// down, the send never lands, one of the other select cases fires, and we
// report back that the grant did NOT happen.
//
// That distinction matters for in-flight bookkeeping — see grantHandler.
func (b *baseRouter) grant(req handlerReq, resp handlerResp) bool {
	select {
	case req.respond <- resp:
		return true
	case <-req.ctx.Done():
		return false
	case <-b.shutdownCtx.Done():
		return false
	}
}

// grantHandler is the "this caller can now use process p" path. It does
// two things that must stay locked together:
//
//  1. Hand the caller a wrapped p.ServeHTTP (via trackedServe) so when the
//     HTTP request finishes, the run loop hears about it.
//  2. Bump inFlight[modelID] so the router knows this process is busy and
//     refuses to evict it until the count comes back down.
//
// The increment is gated on grant() returning true. If grant() returns
// false, the caller already walked away and trackedServe will never run —
// which means no matching decrement will ever arrive on serveDoneCh.
// Incrementing in that case would strand the counter at >0 forever and
// the router would never again be willing to swap this model out.
//
// In short: increment if and only if we know a decrement is coming.
func (b *baseRouter) grantHandler(req handlerReq, modelID string, p process.Process, inFlight map[string]int) {
	if b.grant(req, handlerResp{handleFunc: b.trackedServe(modelID, p)}) {
		inFlight[modelID]++
	}
}

// trackedServe is the wrapper that closes the loop on in-flight tracking.
// It runs p.ServeHTTP normally; the only added behaviour is a deferred
// send on serveDoneCh after the handler returns. That send is what tells
// the run loop "this model now has one fewer request in flight — go look
// at the queue again, you may be able to start a swap you previously had
// to defer."
//
// The select on shutdownCtx.Done() is a release valve: if the router is
// already shutting down, nobody is reading serveDoneCh, so we drop the
// notification rather than blocking the HTTP goroutine forever.
func (b *baseRouter) trackedServe(modelID string, p process.Process) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			select {
			case b.serveDoneCh <- serveDoneEvent{modelID: modelID}:
			case <-b.shutdownCtx.Done():
			}
		}()
		p.ServeHTTP(w, r)
	}
}

// handleRequest decides what to do with one incoming ServeHTTP request. It is
// called from run() and never blocks indefinitely: any work that has to wait
// (starting a process, stopping siblings, waiting for ready) is deferred to
// a swap goroutine and reported back via swapDoneCh.
//
// The decision tree, in order:
//
//  1. Unknown model — respond with ErrNoLocalModelFound and move on.
//  2. A swap to the same model is already in flight — attach this waiter so
//     one swap serves all callers that asked for the same model.
//  3. Fast path — the target process is already ready, the planner sees
//     nothing to evict, and no in-flight swap is evicting it. Hand back its
//     ServeHTTP immediately (wrapped so the run loop knows when it ends).
//  4. Would collide with an in-flight swap (we'd stop their target, or
//     they're stopping us) — park in the queue for handleSwapDone to drain.
//  5. Would evict a process that is still handling requests — park in the
//     queue. handleServeDone will retry when the busy process drains.
//  6. Otherwise — start a new swap. This may run in parallel with other
//     active swaps when their evict sets don't intersect.
func (b *baseRouter) handleRequest(req handlerReq, active map[string]*activeSwap, inFlight map[string]int, queued *[]handlerReq) {
	// (1) Unknown model.
	p, ok := b.processes[req.model]
	if !ok {
		b.logger.Debugf("%s: model %s not handled by this router", b.name, req.model)
		b.grant(req, handlerResp{err: ErrNoLocalModelFound})
		return
	}

	// (2) Join an in-flight swap for the same model.
	if s, ok := active[req.model]; ok {
		b.logger.Debugf("%s: joining in-flight swap for model %s (%d waiters)", b.name, req.model, len(s.waiters)+1)
		s.waiters = append(s.waiters, req)
		return
	}

	evict := b.planner.EvictionFor(req.model, activeTargets(active, req.model))

	// (3) Fast path: ready, nothing to evict, and nobody is evicting us.
	if p.State() == process.StateReady && len(evict) == 0 && !collidesWith(req.model, evict, active) {
		b.logger.Debugf("%s: fast-path serving model %s (already ready)", b.name, req.model)
		b.grantHandler(req, req.model, p, inFlight)
		return
	}

	// (4) Collision with an in-flight swap — queue.
	if collidesWith(req.model, evict, active) {
		b.logger.Debugf("%s: queuing request for model %s (collides with in-flight swap)", b.name, req.model)
		*queued = append(*queued, req)
		b.broadcastQueuePositions(*queued)
		return
	}

	// (5) Would evict a busy process — queue until it drains.
	if conflictsWithInFlight(evict, inFlight) {
		b.logger.Debugf("%s: queuing request for model %s (would evict in-flight process)", b.name, req.model)
		*queued = append(*queued, req)
		b.broadcastQueuePositions(*queued)
		return
	}

	// (6) Start a new (possibly parallel) swap.
	b.logger.Debugf("%s: starting swap for model %s, evicting %v", b.name, req.model, evict)
	s := b.startSwap(req, evict)
	active[s.modelID] = s
}

// handleSwapDone is called from run() when a swap goroutine reports that it
// has finished. It fans out the result to every waiter that joined this swap,
// removes the swap from the active map, and then walks the queue once,
// promoting any items that no longer collide with the remaining active set.
// FIFO order is preserved: items still blocked stay in place.
func (b *baseRouter) handleSwapDone(ev swapDone, active map[string]*activeSwap, inFlight map[string]int, queued *[]handlerReq) {
	s, ok := active[ev.modelID]
	if !ok {
		return
	}
	delete(active, ev.modelID)

	for _, w := range s.waiters {
		if ev.err != nil {
			b.grant(w, handlerResp{err: ev.err})
		} else {
			p := b.processes[ev.modelID]
			b.grantHandler(w, ev.modelID, p, inFlight)
		}
	}

	b.drainQueue(active, inFlight, queued)
}

// handleServeDone is called from run() each time a tracked ServeHTTP
// finishes. It decrements the per-model in-flight count and, when that
// drops to zero, retries the queue: requests whose swap was deferred
// because they would have evicted this (now-idle) process can now proceed.
func (b *baseRouter) handleServeDone(ev serveDoneEvent, active map[string]*activeSwap, inFlight map[string]int, queued *[]handlerReq) {
	inFlight[ev.modelID]--
	if inFlight[ev.modelID] <= 0 {
		delete(inFlight, ev.modelID)
		b.drainQueue(active, inFlight, queued)
	}
}

// drainQueue walks the queued requests in order, re-running the handleRequest
// decision tree against the (now smaller) active set. Items that can now start
// or join become satisfied; items still blocked remain queued in original
// order so they get another chance on the next swap completion.
func (b *baseRouter) drainQueue(active map[string]*activeSwap, inFlight map[string]int, queued *[]handlerReq) {
	if len(*queued) == 0 {
		return
	}
	pending := *queued
	var remaining []handlerReq
	for _, req := range pending {
		p, ok := b.processes[req.model]
		if !ok {
			b.grant(req, handlerResp{err: ErrNoLocalModelFound})
			continue
		}
		if s, ok := active[req.model]; ok {
			b.logger.Debugf("%s: queued request for model %s now joining in-flight swap", b.name, req.model)
			s.waiters = append(s.waiters, req)
			continue
		}
		evict := b.planner.EvictionFor(req.model, activeTargets(active, req.model))
		if p.State() == process.StateReady && len(evict) == 0 && !collidesWith(req.model, evict, active) {
			b.logger.Debugf("%s: queued request for model %s now served fast-path", b.name, req.model)
			b.grantHandler(req, req.model, p, inFlight)
			continue
		}
		if collidesWith(req.model, evict, active) {
			remaining = append(remaining, req)
			continue
		}
		if conflictsWithInFlight(evict, inFlight) {
			remaining = append(remaining, req)
			continue
		}
		b.logger.Debugf("%s: queued request for model %s now starting swap, evicting %v", b.name, req.model, evict)
		s := b.startSwap(req, evict)
		active[s.modelID] = s
	}
	*queued = remaining
	b.broadcastQueuePositions(*queued)
}

// broadcastQueuePositions sends each queued request its current 1-indexed
// position. Sends are non-blocking: if the channel is full, the old value is
// drained first so the consumer always sees the latest position.
func (b *baseRouter) broadcastQueuePositions(queued []handlerReq) {
	for i, req := range queued {
		pos := i + 1
		select {
		case req.positionCh <- pos:
		default:
			select {
			case <-req.positionCh:
			default:
			}
			select {
			case req.positionCh <- pos:
			default:
			}
		}
	}
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

// conflictsWithInFlight reports whether any model in evict is still handling
// requests. Stopping a busy process would cancel its callers' connections,
// so the router defers the swap until those callers finish.
func conflictsWithInFlight(evict []string, inFlight map[string]int) bool {
	for _, m := range evict {
		if inFlight[m] > 0 {
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

	// Cancel shutdownCtx first so any waiter that is currently parked on
	// its respond channel can exit via its own shutdownCtx.Done() branch.
	// The grant calls below then either land (waiter happened to receive
	// before noticing shutdown) or fall through immediately via grant's
	// shutdownCtx case — either way the waiter sees a non-OK response.
	b.shutdownFn()

	for _, s := range active {
		for _, w := range s.waiters {
			b.grant(w, handlerResp{err: shutdownErr})
		}
	}
	for _, w := range queued {
		b.grant(w, handlerResp{err: shutdownErr})
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
			<-done
		}
	} else {
		<-done
	}

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
// It blocks until each targeted process has stopped.
//
// The request is funneled through the run loop so eviction is coordinated
// with the rest of the router's state: pending swap waiters for an
// unloaded model are released with an error, queued requests for unloaded
// models are dropped, and any deferred swaps that were waiting on those
// models become eligible to start.
//
// In-flight requests being served by an unloaded process are not waited
// for — Stop kills the upstream, those callers see whatever error the
// reverse proxy surfaces and may retry. Their trackedServe defers fire
// normally and decrement inFlight as the dying handlers return.
func (b *baseRouter) Unload(timeout time.Duration, models ...string) {
	targets := models
	if len(targets) == 0 {
		targets = make([]string, 0, len(b.processes))
		for id := range b.processes {
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 {
		return
	}

	req := unloadReq{targets: targets, timeout: timeout, respond: make(chan struct{})}
	select {
	case b.unloadCh <- req:
	case <-b.runDone:
		return
	}
	<-req.respond
}

// handleUnload runs on the run loop in response to an Unload call. It
// reconciles router-owned state with the impending Stop, then performs
// the Stop synchronously so callers of Unload remain blocked until each
// targeted process has actually exited.
func (b *baseRouter) handleUnload(req unloadReq, active map[string]*activeSwap, inFlight map[string]int, queued *[]handlerReq) {
	unloadErr := fmt.Errorf("%s: model unloaded", b.name)

	targetSet := make(map[string]bool, len(req.targets))
	for _, id := range req.targets {
		targetSet[id] = true
	}

	// Release waiters of any in-flight swap whose target is being
	// unloaded. The swap goroutine itself is left to finish on its own;
	// when its swapDone arrives, handleSwapDone will find no entry in
	// active and silently drop it.
	for id := range targetSet {
		s, ok := active[id]
		if !ok {
			continue
		}
		for _, w := range s.waiters {
			b.grant(w, handlerResp{err: unloadErr})
		}
		delete(active, id)
	}

	// Drop queued requests addressed to unloaded models. Requests for
	// other models stay queued and may benefit from drainQueue at the end.
	if len(*queued) > 0 {
		kept := (*queued)[:0]
		for _, w := range *queued {
			if targetSet[w.model] {
				b.grant(w, handlerResp{err: unloadErr})
				continue
			}
			kept = append(kept, w)
		}
		*queued = kept
	}

	// Stop the targeted processes. Done synchronously so Unload's caller
	// can rely on "after Unload returns, the process is stopped". inFlight
	// is intentionally NOT cleared here: each dying handler will fire its
	// trackedServe defer and reach handleServeDone in the normal way once
	// the run loop is free again.
	var wg sync.WaitGroup
	for id := range targetSet {
		p, ok := b.processes[id]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(id string, p process.Process) {
			defer wg.Done()
			if err := p.Stop(req.timeout); err != nil {
				b.logger.Warnf("%s: unloading %s failed: %v", b.name, id, err)
			}
		}(id, p)
	}
	wg.Wait()

	// Removing entries from active above may have unblocked queued
	// requests that previously collided with the now-cancelled swaps.
	b.drainQueue(active, inFlight, queued)

	close(req.respond)
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

	data, err := FetchContext(req, b.config)
	if err != nil {
		SendError(w, req, err)
		return
	}

	hr := handlerReq{
		model: data.ModelID,
		ctx:   req.Context(),
		// Unbuffered: a successful send on respond proves the waiter is
		// alive and consuming. grant() relies on this to avoid handing a
		// handleFunc to a cancelled waiter and leaking the inFlight count.
		respond:    make(chan handlerResp),
		positionCh: make(chan int, 1),
	}

	select {
	case b.handlerCh <- hr:
	case <-req.Context().Done():
		return
	case <-b.shutdownCtx.Done():
		SendError(w, req, fmt.Errorf("%s is shutting down", b.name))
		return
	}

	isModelReady := false
	if p, ok := b.processes[data.ModelID]; ok {
		isModelReady = p.State() == process.StateReady
	}
	shouldShowLoading := data.Streaming && data.SendLoadingState && isLoadingPath(req.URL.Path) && !isModelReady

	var lw *loadingWriter
	cancelLoad := func() {}
	if shouldShowLoading {
		var swapCtx context.Context
		swapCtx, cancelLoad = context.WithCancel(req.Context())
		lw = newLoadingWriter(b.logger, data.ModelID, w, req)
		go lw.start(swapCtx)
		go func() {
			for {
				select {
				case pos := <-hr.positionCh:
					lw.setUpdate(fmt.Sprintf("Queue position: #%d", pos))
				case <-swapCtx.Done():
					return
				}
			}
		}()
	}

	var resp handlerResp
	select {
	case resp = <-hr.respond:
		cancelLoad()
		if lw != nil {
			lw.waitForCompletion(1 * time.Second)
		}
	case <-req.Context().Done():
		cancelLoad()
		if lw != nil {
			lw.waitForCompletion(1 * time.Second)
		}
		return
	case <-b.shutdownCtx.Done():
		cancelLoad()
		if lw != nil {
			lw.waitForCompletion(1 * time.Second)
		}
		SendError(w, req, fmt.Errorf("%s is shutting down", b.name))
		return
	}

	if resp.err != nil {
		SendError(w, req, resp.err)
		return
	}
	resp.handleFunc(w, req)
}
