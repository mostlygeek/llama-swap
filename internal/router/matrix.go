package router

import (
	"fmt"
	"slices"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

type Matrix struct {
	*baseRouter
}

// NewMatrix builds the matrix router: it compiles the configured matrix,
// wires the solver into a swapper (with the lru idle source bound to the
// process table), and creates one process per configured model.
func NewMatrix(conf config.Config, logs *logmon.Group) (*Matrix, error) {
	mtx := conf.Routing.Router.Settings.Matrix
	if mtx == nil {
		return nil, fmt.Errorf("matrix router requires a matrix configuration")
	}
	if mtx.Program() == nil {
		if err := config.ValidateMatrix(mtx, conf.Models); err != nil {
			return nil, fmt.Errorf("compiling matrix configuration: %w", err)
		}
	}

	// The process table exists before the swapper so the lru tie-breaker
	// can read idle ages from it at decision time (the closure captures the
	// map, which is populated below).
	processes := make(map[string]process.Process, len(conf.Models))
	swapper := &matrixSwapper{
		solver: newMatrixSolver(mtx.Program(), mtx.ResolvedEvictCosts(), mtx.EvictionTieBreaker),
		logger: logs.ProxyLogs,
		idleOf: func(id string) time.Duration {
			proc, ok := processes[id]
			if !ok {
				return 0
			}
			return processIdle(proc)
		},
	}

	base, err := newBaseRouter("matrix", conf, processes, logs.ProxyLogs, swapper)
	if err != nil {
		return nil, fmt.Errorf("creating base router: %w", err)
	}

	for mid, modelCfg := range conf.Models {
		procLog := logmon.NewWriter(logs.UpstreamLogs)
		p, err := process.New(base.procCtx, mid, modelCfg, procLog, logs.ProxyLogs)
		if err != nil {
			base.shutdownFn()
			base.procCancel()
			return nil, fmt.Errorf("creating process for %q: %w", mid, err)
		}
		processes[mid] = p
	}

	r := &Matrix{baseRouter: base}
	go base.run()
	return r, nil
}

// matrixSwapper decides evictions by asking the matrix solver against the
// running set the scheduler hands it.
//
// The scheduler drives planners from a single event-loop goroutine and calls
// OnSwapStart with the same target and running set it just gave EvictionFor,
// so the last decision is cached and reused instead of solving twice per
// swap. The cache is only valid under that single-goroutine access pattern,
// and only in lexical mode: an lru decision depends on idle ages, which move
// between calls, so lru solves are never cached.
type matrixSwapper struct {
	solver *matrixSolver
	logger *logmon.Monitor
	// idleOf reports how long a running model has gone without finishing a
	// request; wired to the router's process table by NewMatrix. Only used
	// by the lru tie-breaker.
	idleOf func(id string) time.Duration

	lastTarget  string
	lastRunning []string
	lastResult  solveResult
	lastValid   bool
}

// solve returns the swap decision for target among running. Lexical
// decisions are cached on (target, running); lru decisions are never
// cached because they depend on idle ages, which move between calls.
func (p *matrixSwapper) solve(target string, running []string) solveResult {
	lru := p.solver.tieBreaker == config.EvictionTieBreakerLRU
	if !lru && p.lastValid && p.lastTarget == target && slices.Equal(p.lastRunning, running) {
		return p.lastResult
	}
	result := p.solver.Solve(target, running, p.idleSnapshot(running))
	if !lru {
		p.lastTarget = target
		p.lastRunning = slices.Clone(running)
		p.lastResult = result
		p.lastValid = true
	}
	return result
}

// idleSnapshot captures the idle age of every running model for one lru
// solve; it returns nil in lexical mode, where the solver ignores it.
func (p *matrixSwapper) idleSnapshot(running []string) map[string]time.Duration {
	if p.solver.tieBreaker != config.EvictionTieBreakerLRU || p.idleOf == nil {
		return nil
	}
	idle := make(map[string]time.Duration, len(running))
	for _, id := range running {
		idle[id] = p.idleOf(id)
	}
	return idle
}

func (p *matrixSwapper) EvictionFor(target string, running []string) []string {
	return p.solve(target, running).Evict
}

// OnSwapStart re-derives the decision the scheduler is acting on and logs
// it (evictions, a cold start, or an already-running target).
func (p *matrixSwapper) OnSwapStart(target string, running []string) {
	result := p.solve(target, running)
	switch {
	case len(result.Evict) > 0:
		p.logger.Infof("matrix: model=%s set=%s dsl=%q evict=%v target=%v cost=%d",
			target, result.SetName, result.DSL, result.Evict, result.TargetSet, result.TotalCost)
	case len(running) == 0:
		p.logger.Infof("matrix: model=%s starting (no models running)", target)
	default:
		p.logger.Debugf("matrix: model=%s already running in set=%s dsl=%q", target, result.SetName, result.DSL)
	}
}

// processIdle reports how long a process has been idle, for the lru
// eviction tie-breaker. Anything that is not a ready, unbusy model is
// reported as freshly busy (zero):
//
//   - a model that is not ready (still loading, or stopping) is
//     mid-transition, and its LastUse baseline is the epoch, which would
//     read as "idle since before time" and make lru prefer it over every
//     genuinely idle model. The scheduler cannot evict a model mid-load,
//     so ranking it first only defers the swap;
//   - a ready model with in-flight requests is busy even though ready:
//     LastUse advances only when a request completes, so a long-running
//     request would age the baseline and rank the busy model above a
//     genuinely idle, equally costly one. The scheduler cannot evict a
//     model mid-request either, so naming it would defer the swap;
//   - a ready model whose baseline was never set (zero LastUse) would
//     read as "idle since before time" and outrank every real model.
//
// Reporting all of these as freshly busy lets lru name an evictable model
// instead, and the swap starts now.
func processIdle(proc process.Process) time.Duration {
	if proc.State() != process.StateReady || proc.InFlight() > 0 || proc.LastUse().IsZero() {
		return 0
	}
	if d := time.Since(proc.LastUse()); d > 0 {
		return d
	}
	return 0
}
