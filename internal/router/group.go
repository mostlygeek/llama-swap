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

type Group struct {
	config       config.Config
	modelToGroup map[string]string
	processes    map[string]process.Process
	logger       *logmon.Monitor

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

func (g *Group) notifyProcessed() {
	if g.testProcessed != nil {
		g.testProcessed <- struct{}{}
	}
}

func NewGroup(conf config.Config, proxylog, upstreamlog *logmon.Monitor) (*Group, error) {
	modelToGroup := make(map[string]string)
	for gid, gcfg := range conf.Groups {
		for _, mid := range gcfg.Members {
			if existing, dup := modelToGroup[mid]; dup {
				return nil, fmt.Errorf("model %q is in multiple groups: %q and %q", mid, existing, gid)
			}
			modelToGroup[mid] = gid
		}
	}

	shutdownCtx, shutdownFn := context.WithCancel(context.Background())

	processes := make(map[string]process.Process, len(modelToGroup))
	for mid := range modelToGroup {
		modelCfg, _, ok := conf.FindConfig(mid)
		if !ok {
			shutdownFn()
			return nil, fmt.Errorf("no model config for %q", mid)
		}
		procLog := logmon.NewWriter(upstreamlog)
		p, err := process.New(shutdownCtx, mid, modelCfg, procLog, proxylog)
		if err != nil {
			shutdownFn()
			return nil, fmt.Errorf("creating process for %q: %w", mid, err)
		}
		processes[mid] = p
	}

	g := &Group{
		config:       conf,
		modelToGroup: modelToGroup,
		processes:    processes,
		logger:       proxylog,
		shutdownCtx:  shutdownCtx,
		shutdownFn:   shutdownFn,
		handlerCh:    make(chan handlerReq),
		shutdownCh:   make(chan shutdownReq),
		swapDoneCh:   make(chan swapDone),
		runDone:      make(chan struct{}),
	}
	go g.run()
	return g, nil
}

func (g *Group) run() {
	defer close(g.runDone)

	var active *activeSwap
	var queued []handlerReq

	for {
		select {
		case req := <-g.shutdownCh:
			g.handleShutdown(req, active, queued)
			return

		case req := <-g.handlerCh:
			active = g.handleRequest(req, active, &queued)
			g.notifyProcessed()

		case ev := <-g.swapDoneCh:
			active = g.handleSwapDone(ev, active, &queued)
			g.notifyProcessed()
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
//  2. Fast path — the target process is already ready and nothing else needs
//     to be stopped for it to serve. Hand back its ServeHTTP immediately.
//  3. A swap to the same model is already in flight — attach this waiter to
//     it so one swap serves all callers that asked for the same model.
//  4. A swap to a different model is in flight — park this request in the
//     queue. handleSwapDone() will promote it once the current swap finishes.
//  5. Nothing in flight — kick off a new swap for this model.
func (g *Group) handleRequest(req handlerReq, active *activeSwap, queued *[]handlerReq) *activeSwap {
	// (1) Unknown model.
	p, ok := g.processes[req.model]
	if !ok {
		req.respond <- handlerResp{err: ErrNoLocalModelFound}
		return active
	}

	// (2) Fast path: ready and no conflicting processes need to be stopped.
	if active == nil && p.State() == process.StateReady && len(g.stopSet(req.model)) == 0 {
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
	return g.startSwap(req)
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
func (g *Group) handleSwapDone(ev swapDone, active *activeSwap, queued *[]handlerReq) *activeSwap {
	if active == nil {
		return nil
	}

	// (1) Notify everyone who was waiting on this swap.
	for _, w := range active.waiters {
		if ev.err != nil {
			w.respond <- handlerResp{err: ev.err}
		} else {
			p := g.processes[ev.modelID]
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
	newSwap := g.startSwap(next)

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

func (g *Group) startSwap(initial handlerReq) *activeSwap {
	swap := &activeSwap{
		modelID: initial.model,
		waiters: []handlerReq{initial},
	}
	toStop := g.stopSet(initial.model)
	go g.doSwap(initial.model, toStop)
	return swap
}

func (g *Group) doSwap(modelID string, toStop []string) {
	timeout := g.healthCheckTimeout()

	var wg sync.WaitGroup
	for _, mID := range toStop {
		wg.Add(1)
		go func(p process.Process, id string) {
			defer wg.Done()
			if err := p.Stop(timeout); err != nil {
				g.logger.Warnf("group: stopping %s failed: %v", id, err)
			}
		}(g.processes[mID], mID)
	}
	wg.Wait()

	target := g.processes[modelID]
	if target.State() == process.StateStopped {
		go func() {
			if err := target.Run(timeout); err != nil {
				g.logger.Warnf("group: running %s exited: %v", modelID, err)
			}
		}()
	}

	err := target.WaitReady(g.shutdownCtx)

	select {
	case g.swapDoneCh <- swapDone{modelID: modelID, err: err}:
	case <-g.shutdownCtx.Done():
	}
}

// stopSet returns the model IDs that must be stopped before the target model
// can run. Same-group siblings are included when the group has swap=true.
// Cross-group members are included only when the target's group is exclusive;
// loading a model from a non-exclusive group leaves running exclusive groups
// alone, matching the gotcha in the original ProcessGroup behaviour.
func (g *Group) stopSet(target string) []string {
	tg := g.modelToGroup[target]
	tgCfg := g.config.Groups[tg]

	var result []string
	for mID, p := range g.processes {
		if mID == target {
			continue
		}
		st := p.State()
		if st == process.StateStopped || st == process.StateShutdown {
			continue
		}
		og := g.modelToGroup[mID]

		switch {
		case og == tg && tgCfg.Swap:
			result = append(result, mID)

		// the previous ProcessGroup behaviour did not unload exclusive groups
		// when loading a non-exclusive model. This maintains that gotcha
		// for backwards compatibility. The newer swap matrix approach does not
		// have this issue.
		case og != tg && tgCfg.Exclusive:
			result = append(result, mID)
		}
	}
	return result
}

func (g *Group) handleShutdown(req shutdownReq, active *activeSwap, queued []handlerReq) {
	shutdownErr := fmt.Errorf("group router is shutting down")
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
		stopTimeout = g.healthCheckTimeout()
	}

	var wg sync.WaitGroup
	for _, p := range g.processes {
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
			g.shutdownFn()
			<-done
		}
	} else {
		<-done
	}

	g.shutdownFn()
	req.respond <- nil
}

func (g *Group) healthCheckTimeout() time.Duration {
	t := time.Duration(g.config.HealthCheckTimeout) * time.Second
	if t <= 0 {
		return 30 * time.Second
	}
	return t
}

func (g *Group) Shutdown(timeout time.Duration) error {
	if !g.shuttingDown.CompareAndSwap(false, true) {
		return fmt.Errorf("shutdown already in progress")
	}
	req := shutdownReq{timeout: timeout, respond: make(chan error, 1)}
	select {
	case g.shutdownCh <- req:
	case <-g.runDone:
		return nil
	}
	return <-req.respond
}

func (g *Group) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if g.shuttingDown.Load() {
		SendError(w, req, fmt.Errorf("group router is shutting down"))
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
	case g.handlerCh <- hr:
	case <-req.Context().Done():
		return
	case <-g.shutdownCtx.Done():
		SendError(w, req, fmt.Errorf("group router is shutting down"))
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
	case <-g.shutdownCtx.Done():
		SendError(w, req, fmt.Errorf("group router is shutting down"))
		return
	}
}
