package router

import (
	"slices"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// matrixSolver contains pure swap-decision logic with no Process dependencies.
// It is safe for concurrent reads after construction.
type matrixSolver struct {
	expandedSets []config.ExpandedSet // all valid model combinations
	evictCosts   map[string]int       // real model name -> eviction cost (default 1)
	modelToSets  map[string][]int     // model name -> indices into expandedSets
}

func newMatrixSolver(expandedSets []config.ExpandedSet, evictCosts map[string]int) *matrixSolver {
	modelToSets := make(map[string][]int)
	for i, es := range expandedSets {
		for _, model := range es.Models {
			modelToSets[model] = append(modelToSets[model], i)
		}
	}

	return &matrixSolver{
		expandedSets: expandedSets,
		evictCosts:   evictCosts,
		modelToSets:  modelToSets,
	}
}

// solveResult describes what the solver decided.
type solveResult struct {
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
func (s *matrixSolver) Solve(requestedModel string, runningModels []string) solveResult {
	if slices.Contains(runningModels, requestedModel) {
		setName, dsl := s.findMatchingSet(requestedModel, runningModels)
		return solveResult{
			TargetSet: runningModels,
			SetName:   setName,
			DSL:       dsl,
		}
	}

	candidateIndices := s.modelToSets[requestedModel]

	// Model not in any set: runs alone, evict everything.
	if len(candidateIndices) == 0 {
		evict := make([]string, len(runningModels))
		copy(evict, runningModels)
		return solveResult{
			Evict:     evict,
			TargetSet: []string{requestedModel},
		}
	}

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

	chosen := s.expandedSets[bestIdx]
	var evict []string
	for _, running := range runningModels {
		if !slices.Contains(chosen.Models, running) {
			evict = append(evict, running)
		}
	}

	return solveResult{
		Evict:     evict,
		TargetSet: chosen.Models,
		SetName:   chosen.SetName,
		DSL:       chosen.DSL,
		TotalCost: bestCost,
	}
}

// findMatchingSet finds the expanded set that contains all running models.
// Returns the set name and DSL, or empty strings if no match.
func (s *matrixSolver) findMatchingSet(requestedModel string, runningModels []string) (string, string) {
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

func (s *matrixSolver) evictCost(model string) int {
	if cost, ok := s.evictCosts[model]; ok {
		return cost
	}
	return 1
}
