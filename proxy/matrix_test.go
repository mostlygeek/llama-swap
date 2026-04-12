package proxy

import (
	"testing"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to build expanded sets for solver tests
func makeExpandedSets(sets ...struct {
	name   string
	models []string
}) []config.ExpandedSet {
	var result []config.ExpandedSet
	for _, s := range sets {
		result = append(result, config.ExpandedSet{
			SetName: s.name,
			Models:  s.models,
		})
	}
	return result
}

func es(name string, models ...string) struct {
	name   string
	models []string
} {
	return struct {
		name   string
		models []string
	}{name, models}
}

func TestMatrixSolver_AlreadyRunning(t *testing.T) {
	solver := NewMatrixSolver(
		makeExpandedSets(es("s1", "a", "b")),
		nil,
	)

	result, err := solver.Solve("a", []string{"a"})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Nil(t, result.TargetSet)
}

func TestMatrixSolver_NotInAnySet_RunsAlone(t *testing.T) {
	solver := NewMatrixSolver(
		makeExpandedSets(es("s1", "a", "b")),
		nil,
	)

	// Model "c" not in any set
	result, err := solver.Solve("c", []string{"a", "b"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, result.Evict)
	assert.Equal(t, []string{"c"}, result.TargetSet)
}

func TestMatrixSolver_NotInAnySet_NothingRunning(t *testing.T) {
	solver := NewMatrixSolver(
		makeExpandedSets(es("s1", "a", "b")),
		nil,
	)

	result, err := solver.Solve("c", []string{})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"c"}, result.TargetSet)
}

func TestMatrixSolver_SingleSet_EvictsNonMembers(t *testing.T) {
	// Set: [a, b]. Request a when b and c are running.
	solver := NewMatrixSolver(
		makeExpandedSets(es("s1", "a", "b")),
		nil,
	)

	result, err := solver.Solve("a", []string{"b", "c"})
	require.NoError(t, err)
	// c is not in the set, so it gets evicted. b is in the set, so it stays.
	assert.Equal(t, []string{"c"}, result.Evict)
	assert.Equal(t, []string{"a", "b"}, result.TargetSet)
}

func TestMatrixSolver_PicksLowestCost(t *testing.T) {
	// Two sets containing model "a":
	// s1: [a, v]  — if v is running, cost=0; if L is running, cost=30
	// s2: [a, L]  — if L is running, cost=0; if v is running, cost=50
	solver := NewMatrixSolver(
		makeExpandedSets(
			es("s1", "a", "v"),
			es("s2", "a", "L"),
		),
		map[string]int{"v": 50, "L": 30},
	)

	// v is running. Switching to a:
	// s1 cost: v is in s1, so 0
	// s2 cost: v is NOT in s2, so 50
	// => pick s1
	result, err := solver.Solve("a", []string{"v"})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"a", "v"}, result.TargetSet)

	// L is running. Switching to a:
	// s1 cost: L is NOT in s1, so 30
	// s2 cost: L is in s2, so 0
	// => pick s2
	result, err = solver.Solve("a", []string{"L"})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"a", "L"}, result.TargetSet)
}

func TestMatrixSolver_TieBreakingByDefinitionOrder(t *testing.T) {
	// Two sets with identical cost. Definition order should win.
	solver := NewMatrixSolver(
		makeExpandedSets(
			es("s1", "a", "x"),
			es("s2", "a", "y"),
		),
		nil,
	)

	// Nothing running, both sets cost 0. s1 is first.
	result, err := solver.Solve("a", []string{})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"a", "x"}, result.TargetSet)
}

func TestMatrixSolver_EvictCostPreservesExpensive(t *testing.T) {
	// Model "v" costs 50 to evict, "m" costs 1 (default).
	// Sets: [g,v], [g,m]
	// Running: v, m. Request g.
	// s1=[g,v]: evict m (cost 1), keep v
	// s2=[g,m]: evict v (cost 50), keep m
	// => pick s1
	solver := NewMatrixSolver(
		makeExpandedSets(
			es("s1", "g", "v"),
			es("s2", "g", "m"),
		),
		map[string]int{"v": 50},
	)

	result, err := solver.Solve("g", []string{"v", "m"})
	require.NoError(t, err)
	assert.Equal(t, []string{"m"}, result.Evict)
	assert.Equal(t, []string{"g", "v"}, result.TargetSet)
}

func TestMatrixSolver_NothingRunning(t *testing.T) {
	solver := NewMatrixSolver(
		makeExpandedSets(
			es("s1", "g", "v"),
			es("s2", "q", "v"),
		),
		nil,
	)

	result, err := solver.Solve("g", []string{})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"g", "v"}, result.TargetSet)
}

func TestMatrixSolver_FullScenario(t *testing.T) {
	// Simulates the example config:
	// standard: [g,v], [q,v], [m,v]
	// with_rerank: [g,v,e], [q,v,e]
	// creative: [g,sd], [q,sd]
	// full: [L]
	solver := NewMatrixSolver(
		makeExpandedSets(
			es("standard", "g", "v"),
			es("standard", "q", "v"),
			es("standard", "m", "v"),
			es("with_rerank", "e", "g", "v"),
			es("with_rerank", "e", "q", "v"),
			es("creative", "g", "sd"),
			es("creative", "q", "sd"),
			es("full", "L"),
		),
		map[string]int{"v": 50, "L": 30, "whisper": 10},
	)

	// Running: g, v. Request q.
	// standard[q,v]: evict g (cost 1), keep v. Total: 1.
	// with_rerank[q,v,e]: evict g (cost 1), keep v. Total: 1.
	// => tie, pick first by definition order = standard[q,v]
	result, err := solver.Solve("q", []string{"g", "v"})
	require.NoError(t, err)
	assert.Equal(t, []string{"g"}, result.Evict)
	assert.Equal(t, []string{"q", "v"}, result.TargetSet)

	// Running: g, v. Request L.
	// full[L]: evict g (cost 1) + v (cost 50). Total: 51.
	// Only one set contains L, so pick it.
	result, err = solver.Solve("L", []string{"g", "v"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"g", "v"}, result.Evict)
	assert.Equal(t, []string{"L"}, result.TargetSet)

	// Running: g, v. Request sd.
	// creative[g,sd]: evict v (cost 50). Total: 50.
	// creative[q,sd]: evict g (cost 1) + v (cost 50). Total: 51.
	// => pick creative[g,sd]
	result, err = solver.Solve("sd", []string{"g", "v"})
	require.NoError(t, err)
	assert.Equal(t, []string{"v"}, result.Evict)
	assert.Equal(t, []string{"g", "sd"}, result.TargetSet)

	// Running: q, v, e. Request g.
	// standard[g,v]: evict q (1) + e (1). Total: 2.
	// with_rerank[g,v,e]: evict q (1). Total: 1.
	// creative[g,sd]: evict q (1) + v (50) + e (1). Total: 52.
	// => pick with_rerank[g,v,e]
	result, err = solver.Solve("g", []string{"e", "q", "v"})
	require.NoError(t, err)
	assert.Equal(t, []string{"q"}, result.Evict)
	assert.Equal(t, []string{"e", "g", "v"}, result.TargetSet)
}
