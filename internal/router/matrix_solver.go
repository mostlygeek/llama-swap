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
	// reclaim selects the eviction objective; see config.Reclaim*.
	reclaim string
}

func newMatrixSolver(program *matrixdsl.Program, costs func(running []string) map[string]int, reclaim string) *matrixSolver {
	return &matrixSolver{
		program: program,
		costs:   costs,
		reclaim: reclaim,
	}
}

type solveResult = matrixdsl.Decision

// Solve decides the evictions for requestedModel among runningModels.
// upcoming is the pending request queue (excluding requestedModel); it is
// consulted only when the solver was built with the queue reclaim objective.
// reserved are the running models in-flight swaps have claimed as their
// target: loading, not idle, and never evicted while an idle alternative
// exists.
func (s *matrixSolver) Solve(requestedModel string, runningModels []string, upcoming, reserved []string) solveResult {
	return s.program.Solve(requestedModel, runningModels, matrixdsl.SolveOptions{
		EvictCosts: s.costs(runningModels),
		Reclaim:    s.reclaim,
		Upcoming:   upcoming,
		Reserved:   reserved,
	})
}
