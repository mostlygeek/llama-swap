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
			// A model that is not ready (still loading, or stopping) is not
			// idle: it is mid-transition, and its LastUse baseline is the
			// epoch, which would otherwise read as "idle since before time"
			// and make lru prefer it over every genuinely idle model. Because
			// the scheduler cannot evict a model mid-load, ranking it first
			// only defers the swap; report it as freshly busy instead so the
			// tie-breaker evicts a ready model and the swap starts now.
			if proc.State() != process.StateReady {
				return 0
			}
			if d := time.Since(proc.LastUse()); d > 0 {
				return d
			}
			return 0
		},
	}

	// Build a process for every model in the config. Any model can run alone
	// even if it is not part of a set; this mirrors proxy.NewMatrix.
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
