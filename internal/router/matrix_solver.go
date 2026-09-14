package router

import (
	"time"

	matrixdsl "github.com/mostlygeek/llama-swap/internal/matrix"
)

// matrixSolver contains pure swap-decision logic with no Process dependencies.
// It is safe for concurrent reads after construction.
type matrixSolver struct {
	program    *matrixdsl.Program
	evictCosts map[string]int
	// tieBreaker orders equal-cost candidates; see config.EvictionTieBreaker*.
	tieBreaker string
}

func newMatrixSolver(program *matrixdsl.Program, evictCosts map[string]int, tieBreaker string) *matrixSolver {
	return &matrixSolver{
		program:    program,
		evictCosts: evictCosts,
		tieBreaker: tieBreaker,
	}
}

type solveResult = matrixdsl.Decision

// Solve decides the evictions for requestedModel among runningModels. idle
// maps a running model to how long it has gone without finishing a request;
// it is consulted only when the solver was built with the lru tie-breaker.
func (s *matrixSolver) Solve(requestedModel string, runningModels []string, idle map[string]time.Duration) solveResult {
	return s.program.Solve(requestedModel, runningModels, matrixdsl.SolveOptions{
		EvictCosts: s.evictCosts,
		TieBreaker: s.tieBreaker,
		Idle:       idle,
	})
}
