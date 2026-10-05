package router

import (
	"fmt"
	"slices"

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

	// Eviction cost = evict_costs multiplier * measured median load time.
	// The tracker observes process state transitions so slow-recovering
	// models become expensive to evict without any operator input.
	costs := newLoadCostTracker(mtx.ResolvedEvictCosts(), logs.ProxyLogs)

	swapper := &matrixSwapper{
		solver: newMatrixSolver(mtx.Program(), costs.EvictCosts, mtx.Reclaim),
		logger: logs.ProxyLogs,
	}

	// Build a process for every model in the config. Any model can run alone
	// even if it is not part of a set; this mirrors proxy.NewMatrix.
	processes := make(map[string]process.Process, len(conf.Models))
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

	costs.attach(base.procCtx)

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
// and only when the decision is a pure function of (target, running):
// a queue-reclaim decision depends on the pending queue, which moves between
// calls, so those solves are never cached while the queue is non-empty.
type matrixSwapper struct {
	solver *matrixSolver
	logger *logmon.Monitor
	// lastUpcoming and lastReserved carry the queue and the in-flight swap
	// targets from EvictionFor into the immediate OnSwapStart re-solve:
	// decisions that depend on them bypass the decision cache, so
	// OnSwapStart must re-solve against the same picture EvictionFor decided
	// on. FIFO calls the two back-to-back on the event-loop goroutine, so
	// the last values are the right ones.
	lastUpcoming []string
	lastReserved []string

	lastTarget  string
	lastRunning []string
	lastResult  solveResult
	lastValid   bool
}

// cacheable reports whether the decision is a pure function of (target,
// running) and may be cached between EvictionFor and OnSwapStart. A
// queue-reclaim decision depends on the pending queue (lastUpcoming, which
// the cache key does not cover), and a decision with in-flight swap targets
// reserved depends on the active set (lastReserved, which it does not
// cover). Queue reclaim with an empty queue and no reserved models behaves
// as minimal and is cacheable again.
func (p *matrixSwapper) cacheable() bool {
	if len(p.lastReserved) > 0 {
		return false
	}
	return p.solver.reclaim == config.ReclaimMinimal || len(p.lastUpcoming) == 0
}

func (p *matrixSwapper) solve(target string, running, upcoming, reserved []string) solveResult {
	if p.cacheable() && p.lastValid && p.lastTarget == target && slices.Equal(p.lastRunning, running) {
		return p.lastResult
	}
	result := p.solver.Solve(target, running, upcoming, reserved)
	if p.cacheable() {
		p.lastTarget = target
		p.lastRunning = slices.Clone(running)
		p.lastResult = result
		p.lastValid = true
	}
	return result
}

func (p *matrixSwapper) EvictionFor(target string, running, upcoming, reserved []string) []string {
	p.lastUpcoming = upcoming
	p.lastReserved = reserved
	return p.solve(target, running, upcoming, reserved).Evict
}

func (p *matrixSwapper) OnSwapStart(target string, running []string) {
	result := p.solve(target, running, p.lastUpcoming, p.lastReserved)
	switch {
	case len(result.Evict) > 0:
		// The reserved list explains the picture the decision was made
		// against: which models were loading (and therefore protected) when
		// the idle model was turned over. It is only printed when non-empty
		// — with no in-flight swaps there is nothing to explain.
		if len(p.lastReserved) > 0 {
			p.logger.Infof("matrix: model=%s set=%s dsl=%q evict=%v target=%v cost=%d reserved=%v",
				target, result.SetName, result.DSL, result.Evict, result.TargetSet, result.TotalCost, p.lastReserved)
		} else {
			p.logger.Infof("matrix: model=%s set=%s dsl=%q evict=%v target=%v cost=%d",
				target, result.SetName, result.DSL, result.Evict, result.TargetSet, result.TotalCost)
		}
	case len(running) == 0:
		p.logger.Infof("matrix: model=%s starting (no models running)", target)
	default:
		p.logger.Debugf("matrix: model=%s already running in set=%s dsl=%q", target, result.SetName, result.DSL)
	}
}
