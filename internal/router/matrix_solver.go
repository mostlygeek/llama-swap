package router

import matrixdsl "github.com/mostlygeek/llama-swap/internal/matrix"

// matrixSolver contains pure swap-decision logic with no Process dependencies.
// It is safe for concurrent reads after construction.
type matrixSolver struct {
	program    *matrixdsl.Program
	evictCosts map[string]int
}

func newMatrixSolver(program *matrixdsl.Program, evictCosts map[string]int) *matrixSolver {
	return &matrixSolver{
		program:    program,
		evictCosts: evictCosts,
	}
}

type solveResult = matrixdsl.Decision

func (s *matrixSolver) Solve(requestedModel string, runningModels []string) solveResult {
	return s.program.Solve(requestedModel, runningModels, s.evictCosts)
}
