package proxy

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"sync"

	"github.com/mostlygeek/llama-swap/proxy/config"
)

// MatrixSolver contains pure swap-decision logic with no Process dependencies.
// It is safe for concurrent reads after construction.
type MatrixSolver struct {
	expandedSets []config.ExpandedSet // all valid model combinations
	evictCosts   map[string]int       // real model name -> eviction cost (default 1)
	modelToSets  map[string][]int     // model name -> indices into expandedSets
}

// NewMatrixSolver builds a solver from expanded sets and eviction costs.
func NewMatrixSolver(expandedSets []config.ExpandedSet, evictCosts map[string]int) *MatrixSolver {
	modelToSets := make(map[string][]int)
	for i, es := range expandedSets {
		for _, model := range es.Models {
			modelToSets[model] = append(modelToSets[model], i)
		}
	}

	return &MatrixSolver{
		expandedSets: expandedSets,
		evictCosts:   evictCosts,
		modelToSets:  modelToSets,
	}
}

// SolveResult describes what the solver decided.
type SolveResult struct {
	Evict     []string // running models that must be stopped
	TargetSet []string // the chosen set of models (for informational purposes)
	SetName   string   // name of the chosen set
	DSL       string   // original DSL expression for the chosen set
	TotalCost int      // total eviction cost
}

// Solve determines which models to evict when a model is requested.
//
// Algorithm:
//  1. If requestedModel is already running, no eviction needed.
//  2. Find all sets containing requestedModel.
//  3. If no sets found, the model runs alone; evict all running models.
//  4. For each candidate set, compute cost = sum of evict_costs for running
//     models NOT in that set.
//  5. Pick lowest cost. Ties broken by definition order (index in expandedSets).
//  6. Return models to evict and the chosen set.
func (s *MatrixSolver) Solve(requestedModel string, runningModels []string) (SolveResult, error) {
	// If already running, nothing to do (but fill in set info for logging)
	if slices.Contains(runningModels, requestedModel) {
		setName, dsl := s.findMatchingSet(requestedModel, runningModels)
		return SolveResult{
			TargetSet: runningModels,
			SetName:   setName,
			DSL:       dsl,
		}, nil
	}

	candidateIndices := s.modelToSets[requestedModel]

	// Model not in any set: runs alone, evict everything
	if len(candidateIndices) == 0 {
		evict := make([]string, len(runningModels))
		copy(evict, runningModels)
		return SolveResult{
			Evict:     evict,
			TargetSet: []string{requestedModel},
		}, nil
	}

	// Find the cheapest candidate set
	bestCost := -1
	bestIdx := -1

	for _, idx := range candidateIndices {
		setModels := s.expandedSets[idx].Models
		cost := 0
		for _, running := range runningModels {
			if !slices.Contains(setModels, running) {
				cost += s.evictCost(running)
			}
		}

		if bestCost < 0 || cost < bestCost || (cost == bestCost && idx < bestIdx) {
			bestCost = cost
			bestIdx = idx
		}
	}

	// Determine which running models to evict
	chosen := s.expandedSets[bestIdx]
	var evict []string
	for _, running := range runningModels {
		if !slices.Contains(chosen.Models, running) {
			evict = append(evict, running)
		}
	}

	return SolveResult{
		Evict:     evict,
		TargetSet: chosen.Models,
		SetName:   chosen.SetName,
		DSL:       chosen.DSL,
		TotalCost: bestCost,
	}, nil
}

// findMatchingSet finds the expanded set that contains all running models.
// Returns the set name and DSL, or empty strings if no match.
func (s *MatrixSolver) findMatchingSet(requestedModel string, runningModels []string) (string, string) {
	for _, idx := range s.modelToSets[requestedModel] {
		set := s.expandedSets[idx]
		allInSet := true
		for _, m := range runningModels {
			if !slices.Contains(set.Models, m) {
				allInSet = false
				break
			}
		}
		if allInSet {
			return set.SetName, set.DSL
		}
	}
	return "", ""
}

func (s *MatrixSolver) evictCost(model string) int {
	if cost, ok := s.evictCosts[model]; ok {
		return cost
	}
	return 1
}

// Matrix manages processes using solver-based swap logic.
type Matrix struct {
	sync.Mutex
	solver         *MatrixSolver
	processes      map[string]*Process // all processes keyed by real model name
	config         config.Config
	proxyLogger    *LogMonitor
	upstreamLogger *LogMonitor

	// inflight tracks ProxyRequest calls that have released m.Lock but may
	// not yet have incremented Process.inFlightRequests. A concurrent
	// request that needs to evict models waits for inflight to drain under
	// m.Lock before stopping anything. Without this, a request that
	// released m.Lock but has not yet reached Process.inFlightRequests.Add(1)
	// races with Stop()'s Wait() and can be killed mid-request.
	inflight sync.WaitGroup

	// testDelayFastPath is a test-only hook invoked in the no-eviction path
	// after m.Lock is released but before the request is dispatched to
	// Process.ProxyRequest. Tests use it to park a request at the exact
	// race window to deterministically reproduce the race.
	testDelayFastPath func()
}

// NewMatrix creates a Matrix from config. It creates a Process for every
// model defined in the config (any model can run alone even if not in a set).
func NewMatrix(cfg config.Config, proxyLogger, upstreamLogger *LogMonitor) *Matrix {
	processes := make(map[string]*Process)
	for modelID, modelConfig := range cfg.Models {
		processLogger := NewLogMonitorWriter(upstreamLogger)
		process := NewProcess(modelID, cfg.HealthCheckTimeout, modelConfig, processLogger, proxyLogger)
		processes[modelID] = process
	}

	evictCosts := cfg.Matrix.ResolvedEvictCosts()

	return &Matrix{
		solver:         NewMatrixSolver(cfg.ExpandedSets, evictCosts),
		processes:      processes,
		config:         cfg,
		proxyLogger:    proxyLogger,
		upstreamLogger: upstreamLogger,
	}
}

// ProtocolsFor returns the declared native protocols for modelID. Matrix
// models map 1:1 to real models in config, so this is just a direct lookup.
func (m *Matrix) ProtocolsFor(modelID string) []string {
	if mc, ok := m.config.Models[modelID]; ok {
		return mc.Protocols
	}
	return nil
}

// ProxyRequest handles the swap logic and proxies the request to the model.
func (m *Matrix) ProxyRequest(modelID string, w http.ResponseWriter, r *http.Request) error {
	process, ok := m.processes[modelID]
	if !ok {
		return fmt.Errorf("model %s not found in matrix", modelID)
	}

	m.Lock()
	running := m.runningModels()
	result, err := m.solver.Solve(modelID, running)
	if err != nil {
		m.Unlock()
		return fmt.Errorf("matrix solver error: %w", err)
	}

	// Log solver decision
	if len(result.Evict) > 0 {
		m.proxyLogger.Infof("Matrix: model=%s set=%s dsl=%q evict=%v target=%v cost=%d",
			modelID, result.SetName, result.DSL, result.Evict, result.TargetSet, result.TotalCost)
	} else if len(running) == 0 {
		m.proxyLogger.Infof("Matrix: model=%s starting (no models running)", modelID)
	} else {
		m.proxyLogger.Debugf("Matrix: model=%s already running in set=%s dsl=%q", modelID, result.SetName, result.DSL)
	}

	// Evict models that need to be stopped
	if len(result.Evict) > 0 {
		// Wait for any in-flight ProxyRequest calls to register on their
		// Process before stopping anything. Without this, a request that
		// released m.Lock but has not yet incremented
		// Process.inFlightRequests races with Stop() and can be killed
		// mid-request.
		m.inflight.Wait()

		var wg sync.WaitGroup
		for _, evictModel := range result.Evict {
			if p, exists := m.processes[evictModel]; exists {
				wg.Add(1)
				go func(p *Process) {
					defer wg.Done()
					p.Stop()
				}(p)
			}
		}
		wg.Wait()
	}

	// Register this request in inflight before releasing m.Lock so a
	// concurrent eviction will wait for it to complete.
	m.inflight.Add(1)
	defer m.inflight.Done()
	isFastPath := len(result.Evict) == 0
	m.Unlock()

	if isFastPath && m.testDelayFastPath != nil {
		m.testDelayFastPath()
	}

	// Proxy the request (Process handles on-demand start)
	process.ProxyRequest(w, r)
	return nil
}

// StopProcesses stops all running processes.
func (m *Matrix) StopProcesses(strategy StopStrategy) {
	m.Lock()
	defer m.Unlock()

	var wg sync.WaitGroup
	for _, process := range m.processes {
		wg.Add(1)
		go func(p *Process) {
			defer wg.Done()
			switch strategy {
			case StopImmediately:
				p.StopImmediately()
			default:
				p.Stop()
			}
		}(process)
	}
	wg.Wait()
}

// StopProcess stops a single process by model ID.
func (m *Matrix) StopProcess(modelID string, strategy StopStrategy) error {
	process, ok := m.processes[modelID]
	if !ok {
		return fmt.Errorf("process not found for %s", modelID)
	}

	switch strategy {
	case StopImmediately:
		process.StopImmediately()
	default:
		process.Stop()
	}
	return nil
}

// Shutdown shuts down all processes.
func (m *Matrix) Shutdown() {
	var wg sync.WaitGroup
	for _, process := range m.processes {
		wg.Add(1)
		go func(p *Process) {
			defer wg.Done()
			p.Shutdown()
		}(process)
	}
	wg.Wait()
}

// RunningModels returns model names currently in an active (non-stopped) state.
func (m *Matrix) RunningModels() []string {
	m.Lock()
	defer m.Unlock()
	return m.runningModels()
}

// runningModels returns running model names (caller must hold lock).
func (m *Matrix) runningModels() []string {
	var running []string
	for id, process := range m.processes {
		if process.CurrentState() != StateStopped && process.CurrentState() != StateShutdown {
			running = append(running, id)
		}
	}
	sort.Strings(running)
	return running
}

// GetProcess returns the Process for a model.
func (m *Matrix) GetProcess(modelID string) (*Process, bool) {
	p, ok := m.processes[modelID]
	return p, ok
}

// HasModel returns true if the model is managed by this matrix.
func (m *Matrix) HasModel(modelID string) bool {
	_, ok := m.processes[modelID]
	return ok
}
