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
	// If already running, nothing to do
	if slices.Contains(runningModels, requestedModel) {
		return SolveResult{}, nil
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
	chosenSet := s.expandedSets[bestIdx].Models
	var evict []string
	for _, running := range runningModels {
		if !slices.Contains(chosenSet, running) {
			evict = append(evict, running)
		}
	}

	return SolveResult{
		Evict:     evict,
		TargetSet: chosenSet,
	}, nil
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

	// Evict models that need to be stopped
	if len(result.Evict) > 0 {
		m.proxyLogger.Debugf("Matrix: evicting %v to run %s (target set: %v)", result.Evict, modelID, result.TargetSet)
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
	m.Unlock()

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

// RunningModels returns model names currently in StateReady.
func (m *Matrix) RunningModels() []string {
	m.Lock()
	defer m.Unlock()
	return m.runningModels()
}

// runningModels returns running model names (caller must hold lock).
func (m *Matrix) runningModels() []string {
	var running []string
	for id, process := range m.processes {
		if process.CurrentState() == StateReady {
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
