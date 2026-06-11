package router

import (
	"fmt"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/router/scheduler"
)

type Matrix struct {
	*baseRouter
}

func NewMatrix(conf config.Config, proxylog, upstreamlog *logmon.Monitor) (*Matrix, error) {
	mtx := conf.Routing.Router.Settings.Matrix
	if mtx == nil {
		return nil, fmt.Errorf("matrix router requires a matrix configuration")
	}

	swapper := &matrixSwapper{
		solver: newMatrixSolver(mtx.ExpandedSets, mtx.ResolvedEvictCosts()),
		logger: proxylog,
	}

	// Build a process for every model in the config. Any model can run alone
	// even if it is not part of a set; this mirrors proxy.NewMatrix.
	processes := make(map[string]process.Process, len(conf.Models))
	base := newBaseRouter("matrix", conf, processes, proxylog,
		func(name string, logger *logmon.Monitor, eff scheduler.Effects) scheduler.Scheduler {
			return scheduler.NewFIFO(name, logger, swapper, conf.Routing.Scheduler.Settings.Fifo, eff)
		})

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
type matrixSwapper struct {
	solver *matrixSolver
	logger *logmon.Monitor
}

func (p *matrixSwapper) EvictionFor(target string, running []string) []string {
	return p.solver.Solve(target, running).Evict
}

func (p *matrixSwapper) OnSwapStart(target string, running []string) {
	result := p.solver.Solve(target, running)
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
