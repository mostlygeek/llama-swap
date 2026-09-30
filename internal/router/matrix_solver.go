package router

import matrixdsl "github.com/mostlygeek/llama-swap/internal/matrix"

// matrixSolver contains pure swap-decision logic with no Process dependencies.
// It is safe for concurrent reads after construction, provided the cost
// function is.
type matrixSolver struct {
	program *matrixdsl.Program
	// costs builds the eviction cost map for a set of running models at
	// solve time, so measured costs (loadCostTracker) are always fresh
	// rather than snapshotted at construction.
	costs func(running []string) map[string]int
}

func newMatrixSolver(program *matrixdsl.Program, costs func(running []string) map[string]int) *matrixSolver {
	return &matrixSolver{
		program: program,
		costs:   costs,
	}
}

type solveResult = matrixdsl.Decision

func (s *matrixSolver) Solve(requestedModel string, runningModels []string) solveResult {
	return s.program.Solve(requestedModel, runningModels, s.costs(runningModels))
}
