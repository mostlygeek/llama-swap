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

func NewMatrix(conf config.Config, proxylog, upstreamlog *logmon.Monitor) (*Matrix, error) {
	mtx := conf.Routing.Router.Settings.Matrix
	if mtx == nil {
		return nil, fmt.Errorf("matrix router requires a matrix configuration")
	}
	if mtx.Program() == nil {
		if err := config.ValidateMatrix(mtx, conf.Models); err != nil {
			return nil, fmt.Errorf("compiling matrix configuration: %w", err)
		}
	}

	swapper := &matrixSwapper{
		solver: newMatrixSolver(mtx.Program(), mtx.ResolvedEvictCosts()),
		logger: proxylog,
	}

	// Build a process for every model in the config. Any model can run alone
	// even if it is not part of a set; this mirrors proxy.NewMatrix.
	processes := make(map[string]process.Process, len(conf.Models))
	base, err := newBaseRouter("matrix", conf, processes, proxylog, swapper)
	if err != nil {
		return nil, fmt.Errorf("creating base router: %w", err)
	}

	for mid, modelCfg := range conf.Models {
		procLog := logmon.NewWriter(upstreamlog)
		p, err := process.New(base.procCtx, mid, modelCfg, procLog, proxylog)
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
// swap. The cache is only valid under that single-goroutine access pattern.
type matrixSwapper struct {
	solver *matrixSolver
	logger *logmon.Monitor

	lastTarget  string
	lastRunning []string
	lastResult  solveResult
	lastValid   bool
}

func (p *matrixSwapper) solve(target string, running []string) solveResult {
	if p.lastValid && p.lastTarget == target && slices.Equal(p.lastRunning, running) {
		return p.lastResult
	}
	result := p.solver.Solve(target, running)
	p.lastTarget = target
	p.lastRunning = slices.Clone(running)
	p.lastResult = result
	p.lastValid = true
	return result
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
