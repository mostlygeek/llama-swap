package router

import (
	"fmt"
	"sort"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

type Matrix struct {
	*baseRouter
}

func NewMatrix(conf config.Config, proxylog, upstreamlog *logmon.Monitor) (*Matrix, error) {
	if conf.Matrix == nil {
		return nil, fmt.Errorf("matrix router requires a matrix configuration")
	}

	planner := &matrixPlanner{
		solver: newMatrixSolver(conf.ExpandedSets, conf.Matrix.ResolvedEvictCosts()),
		logger: proxylog,
	}

	// Build a process for every model in the config. Any model can run alone
	// even if it is not part of a set; this mirrors proxy.NewMatrix.
	processes := make(map[string]process.Process, len(conf.Models))
	base := newBaseRouter("matrix", conf, processes, planner, proxylog)
	planner.processes = processes

	for mid, modelCfg := range conf.Models {
		procLog := logmon.NewWriter(upstreamlog)
		p, err := process.New(base.shutdownCtx, mid, modelCfg, procLog, proxylog)
		if err != nil {
			base.shutdownFn()
			return nil, fmt.Errorf("creating process for %q: %w", mid, err)
		}
		processes[mid] = p
	}

	r := &Matrix{baseRouter: base}
	go base.run()
	return r, nil
}

// matrixPlanner decides evictions by asking the matrix solver against the
// current running set.
type matrixPlanner struct {
	solver    *matrixSolver
	processes map[string]process.Process
	logger    *logmon.Monitor
}

func (p *matrixPlanner) EvictionFor(target string, alsoRunning []string) []string {
	return p.solver.Solve(target, p.runningSet(alsoRunning)).Evict
}

func (p *matrixPlanner) OnSwapStart(target string) {
	running := p.runningModels()
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

func (p *matrixPlanner) runningModels() []string {
	return p.runningSet(nil)
}

// runningSet returns the union of live processes (State != Stopped/Shutdown)
// and any extra IDs the baseRouter has already committed to loading but which
// the process state machine has not yet reflected.
func (p *matrixPlanner) runningSet(alsoRunning []string) []string {
	seen := make(map[string]struct{}, len(p.processes))
	var running []string
	for id, proc := range p.processes {
		st := proc.State()
		if st == process.StateStopped || st == process.StateShutdown {
			continue
		}
		seen[id] = struct{}{}
		running = append(running, id)
	}
	for _, id := range alsoRunning {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		running = append(running, id)
	}
	sort.Strings(running)
	return running
}
