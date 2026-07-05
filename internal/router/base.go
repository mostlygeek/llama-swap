package router

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/router/scheduler"
	"github.com/mostlygeek/llama-swap/internal/shared"
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

type inFlightQuery struct {
	modelID string
	respond chan int
}

// baseRouter owns the channels, run-loop, and process machinery shared by every
// concrete router. Concrete routers embed *baseRouter and supply a
// scheduler.Swapper describing how eviction sets are decided. baseRouter
// implements scheduler.Effects so the scheduler can call back for side-effects.
type baseRouter struct {
	name      string
	config    config.Config
	processes map[string]process.Process
	logger    *logmon.Monitor
	schedule  scheduler.Scheduler
	// planner is the eviction policy, retained for the read-only can-i-load
	// preflight (CanLoad); the scheduler owns it for live decisions.
	planner scheduler.Swapper

	// memGate, when non-nil, is an optional fail-open admission gate that
	// evicts LRU models before a swap to keep total GPU memory under a
	// configured budget. nil disables it (no budget configured or no perf
	// monitor). See memgate.go.
	memGate *memGate

	// shutdownCtx governs the request machinery: cancelling it tells grant()
	// and ServeHTTP to stop granting and reject callers. It is deliberately
	// separate from procCtx — see procCtx below.
	shutdownCtx  context.Context
	shutdownFn   context.CancelFunc
	shuttingDown atomic.Bool

	// procCtx is the parent context for every managed process and governs
	// process lifetime only. handleShutdown stops processes gracefully via
	// Stop() and cancels procCtx afterwards, so teardown is never a context
	// cancel racing the graceful path (which collapsed the grace to 100ms and
	// let the caller return before children were reaped — see process run loop).
	procCtx    context.Context
	procCancel context.CancelFunc

	handlerCh       chan scheduler.HandlerReq
	cancelCh        chan scheduler.HandlerReq
	shutdownCh      chan shutdownReq
	unloadCh        chan unloadReq
	swapDoneCh      chan scheduler.SwapDone
	serveDoneCh     chan scheduler.ServeDoneEvent
	inFlightQueryCh chan inFlightQuery

	runDone chan struct{}

	serializeGate *serializeGate
	serializeSeq  atomic.Int64

	// leases, when non-nil, is the model-lease table. It backs refuse-don't-break
	// admission (a load that would evict a leased model is refused) and the
	// /leases API. nil disables leases entirely.
	leases *leaseTable

	// testProcessed, when non-nil, receives one event after each handlerReq
	// or swapDone has been fully processed by run(). Tests use it to wait
	// for run() to reach a deterministic state without sleeping. serveDone
	// events are intentionally NOT signalled here so test event counts
	// remain stable.
	testProcessed chan struct{}
}

func newBaseRouter(
	name string,
	conf config.Config,
	processes map[string]process.Process,
	logger *logmon.Monitor,
	planner scheduler.Swapper,
	gate *memGate,
) (*baseRouter, error) {
	shutdownCtx, shutdownFn := context.WithCancel(context.Background())
	procCtx, procCancel := context.WithCancel(context.Background())
	b := &baseRouter{
		name:            name,
		config:          conf,
		processes:       processes,
		logger:          logger,
		memGate:         gate,
		shutdownCtx:     shutdownCtx,
		shutdownFn:      shutdownFn,
		procCtx:         procCtx,
		procCancel:      procCancel,
		handlerCh:       make(chan scheduler.HandlerReq),
		cancelCh:        make(chan scheduler.HandlerReq),
		shutdownCh:      make(chan shutdownReq),
		unloadCh:        make(chan unloadReq),
		swapDoneCh:      make(chan scheduler.SwapDone),
		serveDoneCh:     make(chan scheduler.ServeDoneEvent),
		inFlightQueryCh: make(chan inFlightQuery),
		runDone:         make(chan struct{}),
		planner:         planner,
	}
	if conf.Performance.SerializeInference {
		b.serializeGate = newSerializeGate()
	}
	b.leases = newLeaseTable(conf.Performance.MaxLeaseDuration, conf.Performance.LeaseStatePath)
	if err := b.leases.loadAndReconcile(conf.Performance.LeaseStatePath); err != nil {
		logger.Warnf("%s: could not load lease state from %s: %v", name, conf.Performance.LeaseStatePath, err)
	}
	go b.leases.runSweeper(defaultLeaseSweep)
	if b.memGate != nil {
		b.memGate.inFlight = b.InFlight
		b.memGate.leases = b.leases
	}
	sched, err := scheduler.New(conf, name, logger, planner, b)
	if err != nil {
		// The lease table's sweeper (and persistence writer, if configured) are
		// already running; stop them so a failed construction leaks nothing.
		b.leases.stop()
		return nil, err
	}
	b.schedule = sched
	return b, nil
}

func (b *baseRouter) notifyProcessed() {
	if b.testProcessed != nil {
		b.testProcessed <- struct{}{}
	}
}

func (b *baseRouter) run() {
	defer close(b.runDone)

	// Runtime memory enforcement. Started here rather than in newBaseRouter
	// because the constructors populate b.processes AFTER newBaseRouter
	// returns but BEFORE `go run()`; starting the watchdog any earlier would
	// race that map fill. The map is never mutated once run() starts.
	go b.redlineLoop()

	for {
		select {
		case req := <-b.shutdownCh:
			b.handleShutdown(req)
			return

		case req := <-b.handlerCh:
			b.schedule.OnRequest(req)
			b.notifyProcessed()

		case req := <-b.cancelCh:
			b.schedule.OnCancel(req)
			b.notifyProcessed()

		case req := <-b.unloadCh:
			b.schedule.OnUnload(req.targets, req.timeout)
			close(req.respond)
			b.notifyProcessed()

		case ev := <-b.swapDoneCh:
			b.schedule.OnSwapDone(ev)
			b.notifyProcessed()

		case ev := <-b.serveDoneCh:
			b.schedule.OnServeDone(ev)

		case q := <-b.inFlightQueryCh:
			q.respond <- b.schedule.InFlight(q.modelID)
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
// That distinction matters for in-flight bookkeeping — see GrantServe.
func (b *baseRouter) grant(req scheduler.HandlerReq, resp scheduler.HandlerResp) bool {
	select {
	case req.Respond <- resp:
		return true
	case <-req.Ctx.Done():
		return false
	case <-b.shutdownCtx.Done():
		return false
	}
}

// ModelState implements scheduler.Effects.
func (b *baseRouter) ModelState(modelID string) (process.ProcessState, bool) {
	p, ok := b.processes[modelID]
	if !ok {
		var zero process.ProcessState
		return zero, false
	}
	return p.State(), true
}

// StartSwap implements scheduler.Effects, launching the swap goroutine.
func (b *baseRouter) StartSwap(modelID string, evict []string) {
	go b.doSwap(modelID, evict)
}

// TryClaimEviction implements scheduler.Effects. It enforces refuse-don't-break:
// a nil return means the eviction set holds no live lease and has been claimed
// (doSwap releases the claim once the stops complete); a non-nil return is a
// LeaseBlockedError (503 + blocked_by) that the scheduler grants to the caller
// instead of starting the swap. Leases disabled -> always nil.
func (b *baseRouter) TryClaimEviction(evict []string) error {
	if b.leases == nil || len(evict) == 0 {
		return nil
	}
	if blockers := b.leases.TryClaimEviction(evict); len(blockers) > 0 {
		return &LeaseBlockedError{Model: strings.Join(evict, ","), BlockedBy: blockers}
	}
	return nil
}

// GrantError implements scheduler.Effects.
func (b *baseRouter) GrantError(req scheduler.HandlerReq, err error) {
	b.grant(req, scheduler.HandlerResp{Err: err})
}

// GrantServe implements scheduler.Effects. It hands the caller a wrapped
// p.ServeHTTP (via trackedServe) so the run loop hears about the request
// finishing, and reports whether the caller received it. The scheduler bumps
// its in-flight count only on a true return: if grant() returns false the
// caller already walked away and trackedServe will never run, so no matching
// decrement will ever arrive — incrementing would strand the counter at >0 and
// the router would never again be willing to evict this model.
func (b *baseRouter) GrantServe(req scheduler.HandlerReq, modelID string) bool {
	p := b.processes[modelID]
	return b.grant(req, scheduler.HandlerResp{HandleFunc: b.trackedServe(modelID, p)})
}

// StopProcesses implements scheduler.Effects, stopping the named processes in
// parallel and blocking until all have stopped.
func (b *baseRouter) StopProcesses(timeout time.Duration, ids []string) {
	var wg sync.WaitGroup
	for _, id := range ids {
		p, ok := b.processes[id]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(id string, p process.Process) {
			defer wg.Done()
			if err := p.Stop(timeout); err != nil {
				b.logger.Warnf("%s: stopping %s failed: %v", b.name, id, err)
			}
		}(id, p)
	}
	wg.Wait()
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
			case b.serveDoneCh <- scheduler.ServeDoneEvent{ModelID: modelID}:
			case <-b.shutdownCtx.Done():
			}
		}()

		if b.serializeGate != nil {
			priority := 0
			if data, ok := shared.ReadContext(r.Context()); ok {
				if p, err := strconv.Atoi(data.Metadata["fifo_priority"]); err == nil {
					priority = p
				}
			}
			release, ok := b.serializeGate.acquire(r.Context(), priority, b.serializeSeq.Add(1))
			if !ok {
				return
			}
			defer release()
		}

		p.ServeHTTP(w, r)
	}
}

// validateLeaseHeader enforces the optional X-Llama-Swap-Lease header: a header
// naming a live lease for a different model than the request is an obvious
// mismatch and is rejected; an unknown/expired id is logged and ignored so a
// client that lost its lease (e.g. across a restart) is not hard-failed.
func (b *baseRouter) validateLeaseHeader(r *http.Request, modelID string) error {
	if b.leases == nil {
		return nil
	}
	id := r.Header.Get(LeaseHeader)
	if id == "" {
		return nil
	}
	lease, ok := b.leases.Lookup(id)
	if !ok {
		b.logger.Debugf("%s: request for %s references unknown lease %s; ignoring", b.name, modelID, id)
		return nil
	}
	if lease.Model != modelID {
		return &LeaseMismatchError{LeaseID: id, LeaseModel: lease.Model, RequestModel: modelID}
	}
	b.logger.Debugf("%s: request for %s references lease %s (holder=%s)", b.name, modelID, id, lease.Holder)
	return nil
}

func (b *baseRouter) InFlight(modelID string) int {
	respond := make(chan int, 1)
	select {
	case b.inFlightQueryCh <- inFlightQuery{modelID: modelID, respond: respond}:
	case <-b.runDone:
		return 0
	}
	select {
	case n := <-respond:
		return n
	case <-b.runDone:
		return 0
	}
}

func (b *baseRouter) doSwap(modelID string, toStop []string) {
	timeout := b.healthCheckTimeout()

	// Release the eviction claim the scheduler placed on toStop once this swap
	// finishes stopping them: at that point they are gone and re-leasing them is
	// safe. Deferred so every exit path (including an admission rejection below)
	// clears it. A nil lease table or empty toStop makes this a no-op.
	if b.leases != nil && len(toStop) > 0 {
		defer b.leases.ReleaseEvictionClaim(toStop)
	}

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

	// Live-GTT budget gate: the solver-decided evictions (toStop) are now
	// stopped and their GPU memory freed. Before launching the target, evict
	// additional LRU models if the projected footprint would still exceed the
	// configured GPU budget. This single chokepoint covers the matrix, group,
	// and solo load paths. nil gate (no budget / no monitor) is a no-op. In
	// strict mode an unfittable load is REJECTED here: the error fans out to
	// every waiter as a 503 via OnSwapDone, and the target never launches.
	// A granted admission holds a reservation until the swap completes, so
	// concurrent admissions cannot jointly overshoot the budget.
	if b.memGate != nil {
		if err := b.memGate.EnsureFits(modelID, b.processes, toStop, b.logger); err != nil {
			b.logger.Warnf("%s: admission rejected for %s: %v", b.name, modelID, err)
			select {
			case b.swapDoneCh <- scheduler.SwapDone{ModelID: modelID, Err: err}:
			case <-b.shutdownCtx.Done():
			}
			return
		}
		defer b.memGate.Release(modelID)
	}

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
	case b.swapDoneCh <- scheduler.SwapDone{ModelID: modelID, Err: err}:
	case <-b.shutdownCtx.Done():
	}
}

func (b *baseRouter) handleShutdown(req shutdownReq) {
	shutdownErr := fmt.Errorf("%s is shutting down", b.name)

	// Cancel shutdownCtx first so any waiter that is currently parked on
	// its respond channel can exit via its own shutdownCtx.Done() branch.
	// The OnShutdown grants below then either land (waiter happened to receive
	// before noticing shutdown) or fall through immediately via grant's
	// shutdownCtx case — either way the waiter sees a non-OK response.
	// This does NOT touch processes: their lifetime is procCtx, cancelled
	// only after the graceful Stop() calls below have reaped them.
	b.shutdownFn()

	b.schedule.OnShutdown(shutdownErr)

	if b.leases != nil {
		b.leases.stop()
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

	// Every process is stopped (children reaped via Stop()). Cancel procCtx so
	// the process run-loop goroutines exit; they are already StateStopped, so
	// this is a clean no-op kill rather than a forced teardown.
	b.procCancel()

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
		shared.SendError(w, req, fmt.Errorf("%s is shutting down", b.name))
		return
	}

	data, err := shared.FetchContext(req, b.config)
	if err != nil {
		shared.SendError(w, req, err)
		return
	}

	if err := b.validateLeaseHeader(req, data.ModelID); err != nil {
		shared.SendError(w, req, err)
		return
	}

	hr := scheduler.HandlerReq{
		Model: data.ModelID,
		Ctx:   req.Context(),
		// Unbuffered: a successful send on Respond proves the waiter is
		// alive and consuming. grant() relies on this to avoid handing a
		// handleFunc to a cancelled waiter and leaking the inFlight count.
		Respond:    make(chan scheduler.HandlerResp),
		PositionCh: make(chan int, 1),
	}

	select {
	case b.handlerCh <- hr:
	case <-req.Context().Done():
		return
	case <-b.shutdownCtx.Done():
		shared.SendError(w, req, fmt.Errorf("%s is shutting down", b.name))
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
				case pos := <-hr.PositionCh:
					lw.setUpdate(fmt.Sprintf("Queue position: #%d", pos))
				case <-swapCtx.Done():
					return
				}
			}
		}()
	}

	// finishLoading stops the loading stream and fences its goroutine off from
	// the ResponseWriter before the real handler (or ServeHTTP's return)
	// reclaims it. release() must run even when waitForCompletion times out:
	// otherwise a still-streaming goroutine flushes a finalized response and
	// panics on the recycled *bufio.Writer.
	finishLoading := func() {
		cancelLoad()
		if lw != nil {
			lw.waitForCompletion(1 * time.Second)
			lw.release()
		}
	}

	var resp scheduler.HandlerResp
	select {
	case resp = <-hr.Respond:
		finishLoading()
	case <-req.Context().Done():
		finishLoading()
		// Notify the scheduler so it can prune this request from its queue
		// and swap waiters. Without this, a queued request whose client left
		// would sit in the scheduler until drainQueue eventually starts a
		// wasted model load for it.
		select {
		case b.cancelCh <- hr:
		case <-b.shutdownCtx.Done():
		}
		return
	case <-b.shutdownCtx.Done():
		finishLoading()
		shared.SendError(w, req, fmt.Errorf("%s is shutting down", b.name))
		return
	}

	if resp.Err != nil {
		shared.SendError(w, req, resp.Err)
		return
	}
	resp.HandleFunc(w, req)
}
