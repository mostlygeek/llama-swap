package router

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

type MatrixRouter struct {
	config    config.Config
	solver    *matrixSolver
	processes map[string]process.Process
	logger    *logmon.Monitor

	shutdownCtx  context.Context
	shutdownFn   context.CancelFunc
	shuttingDown atomic.Bool

	handlerCh  chan handlerReq
	shutdownCh chan shutdownReq
	swapDoneCh chan swapDone

	runDone chan struct{}

	// testProcessed mirrors Group.testProcessed: when non-nil, receives one
	// event after each handlerReq or swapDone has been fully processed by
	// run(). Tests use it to wait for run() to reach a deterministic state
	// without sleeping.
	testProcessed chan struct{}
}

func (r *MatrixRouter) notifyProcessed() {
	if r.testProcessed != nil {
		r.testProcessed <- struct{}{}
	}
}

func NewMatrix(conf config.Config, proxylog, upstreamlog *logmon.Monitor) (*MatrixRouter, error) {
	if conf.Matrix == nil {
		return nil, fmt.Errorf("matrix router requires a matrix configuration")
	}

	shutdownCtx, shutdownFn := context.WithCancel(context.Background())

	// Build a process for every model in the config. Any model can run alone
	// even if it is not part of a set; this mirrors proxy.NewMatrix.
	processes := make(map[string]process.Process, len(conf.Models))
	for mid, modelCfg := range conf.Models {
		procLog := logmon.NewWriter(upstreamlog)
		p, err := process.New(shutdownCtx, mid, modelCfg, procLog, proxylog)
		if err != nil {
			shutdownFn()
			return nil, fmt.Errorf("creating process for %q: %w", mid, err)
		}
		processes[mid] = p
	}

	r := &MatrixRouter{
		config:      conf,
		solver:      newMatrixSolver(conf.ExpandedSets, conf.Matrix.ResolvedEvictCosts()),
		processes:   processes,
		logger:      proxylog,
		shutdownCtx: shutdownCtx,
		shutdownFn:  shutdownFn,
		handlerCh:   make(chan handlerReq),
		shutdownCh:  make(chan shutdownReq),
		swapDoneCh:  make(chan swapDone),
		runDone:     make(chan struct{}),
	}
	go r.run()
	return r, nil
}

func (r *MatrixRouter) run() {
	defer close(r.runDone)

	var active *activeSwap
	var queued []handlerReq

	for {
		select {
		case req := <-r.shutdownCh:
			r.handleShutdown(req, active, queued)
			return

		case req := <-r.handlerCh:
			active = r.handleRequest(req, active, &queued)
			r.notifyProcessed()

		case ev := <-r.swapDoneCh:
			active = r.handleSwapDone(ev, active, &queued)
			r.notifyProcessed()
		}
	}
}

// handleRequest decides what to do with one incoming ServeHTTP request. See
// Group.handleRequest for the full decision-tree commentary; the matrix
// variant differs only in that the eviction decision is made by the solver
// against the current running set instead of by static group membership.
func (r *MatrixRouter) handleRequest(req handlerReq, active *activeSwap, queued *[]handlerReq) *activeSwap {
	p, ok := r.processes[req.model]
	if !ok {
		req.respond <- handlerResp{err: ErrNoLocalModelFound}
		return active
	}

	if active == nil && p.State() == process.StateReady && len(r.solver.Solve(req.model, r.runningModels()).Evict) == 0 {
		req.respond <- handlerResp{handleFunc: p.ServeHTTP}
		return active
	}

	if active != nil && active.modelID == req.model {
		active.waiters = append(active.waiters, req)
		return active
	}

	if active != nil {
		*queued = append(*queued, req)
		return active
	}

	return r.startSwap(req)
}

// handleSwapDone is the matrix-router analogue of Group.handleSwapDone.
// Promoting the next queued request triggers a fresh solver call, so the
// eviction plan reflects the post-swap running set.
func (r *MatrixRouter) handleSwapDone(ev swapDone, active *activeSwap, queued *[]handlerReq) *activeSwap {
	if active == nil {
		return nil
	}

	for _, w := range active.waiters {
		if ev.err != nil {
			w.respond <- handlerResp{err: ev.err}
		} else {
			p := r.processes[ev.modelID]
			w.respond <- handlerResp{handleFunc: p.ServeHTTP}
		}
	}

	if len(*queued) == 0 {
		return nil
	}

	next := (*queued)[0]
	remaining := (*queued)[1:]
	newSwap := r.startSwap(next)

	var leftover []handlerReq
	for _, w := range remaining {
		if w.model == newSwap.modelID {
			newSwap.waiters = append(newSwap.waiters, w)
		} else {
			leftover = append(leftover, w)
		}
	}
	*queued = leftover
	return newSwap
}

func (r *MatrixRouter) startSwap(initial handlerReq) *activeSwap {
	swap := &activeSwap{
		modelID: initial.model,
		waiters: []handlerReq{initial},
	}

	running := r.runningModels()
	result := r.solver.Solve(initial.model, running)
	r.logSolverDecision(initial.model, running, result)

	go r.doSwap(initial.model, result.Evict)
	return swap
}

func (r *MatrixRouter) logSolverDecision(modelID string, running []string, result solveResult) {
	switch {
	case len(result.Evict) > 0:
		r.logger.Infof("matrix: model=%s set=%s dsl=%q evict=%v target=%v cost=%d",
			modelID, result.SetName, result.DSL, result.Evict, result.TargetSet, result.TotalCost)
	case len(running) == 0:
		r.logger.Infof("matrix: model=%s starting (no models running)", modelID)
	default:
		r.logger.Debugf("matrix: model=%s already running in set=%s dsl=%q", modelID, result.SetName, result.DSL)
	}
}

func (r *MatrixRouter) doSwap(modelID string, toStop []string) {
	timeout := r.healthCheckTimeout()

	var wg sync.WaitGroup
	for _, mID := range toStop {
		wg.Add(1)
		go func(p process.Process, id string) {
			defer wg.Done()
			if err := p.Stop(timeout); err != nil {
				r.logger.Warnf("matrix: stopping %s failed: %v", id, err)
			}
		}(r.processes[mID], mID)
	}
	wg.Wait()

	target := r.processes[modelID]
	if target.State() == process.StateStopped {
		go func() {
			if err := target.Run(timeout); err != nil {
				r.logger.Warnf("matrix: running %s exited: %v", modelID, err)
			}
		}()
	}

	err := target.WaitReady(r.shutdownCtx)

	select {
	case r.swapDoneCh <- swapDone{modelID: modelID, err: err}:
	case <-r.shutdownCtx.Done():
	}
}

// runningModels returns the IDs of models whose process is in a non-stopped
// state. Called only from run() and from goroutines that read process state
// without mutating router state.
func (r *MatrixRouter) runningModels() []string {
	var running []string
	for id, p := range r.processes {
		st := p.State()
		if st == process.StateStopped || st == process.StateShutdown {
			continue
		}
		running = append(running, id)
	}
	sort.Strings(running)
	return running
}

func (r *MatrixRouter) handleShutdown(req shutdownReq, active *activeSwap, queued []handlerReq) {
	shutdownErr := fmt.Errorf("matrix router is shutting down")
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
		stopTimeout = r.healthCheckTimeout()
	}

	var wg sync.WaitGroup
	for _, p := range r.processes {
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
			r.shutdownFn()
			<-done
		}
	} else {
		<-done
	}

	r.shutdownFn()
	req.respond <- nil
}

func (r *MatrixRouter) healthCheckTimeout() time.Duration {
	t := time.Duration(r.config.HealthCheckTimeout) * time.Second
	if t <= 0 {
		return 30 * time.Second
	}
	return t
}

func (r *MatrixRouter) Shutdown(timeout time.Duration) error {
	if !r.shuttingDown.CompareAndSwap(false, true) {
		return fmt.Errorf("shutdown already in progress")
	}
	req := shutdownReq{timeout: timeout, respond: make(chan error, 1)}
	select {
	case r.shutdownCh <- req:
	case <-r.runDone:
		return nil
	}
	return <-req.respond
}

func (r *MatrixRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.shuttingDown.Load() {
		SendError(w, req, fmt.Errorf("matrix router is shutting down"))
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
	case r.handlerCh <- hr:
	case <-req.Context().Done():
		return
	case <-r.shutdownCtx.Done():
		SendError(w, req, fmt.Errorf("matrix router is shutting down"))
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
	case <-r.shutdownCtx.Done():
		SendError(w, req, fmt.Errorf("matrix router is shutting down"))
		return
	}
}
