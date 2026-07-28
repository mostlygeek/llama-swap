package router

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
)

var benchmarkSolveResult solveResult

func BenchmarkMatrixSolver_Solve(b *testing.B) {
	for _, dimensions := range []int{2, 3, 4, 5} {
		solver := benchmarkCompiledMatrix(b, dimensions, 10)
		combinations := benchmarkIntPow(10, dimensions)
		running := make([]string, 0, dimensions+1)
		for dimension := range dimensions {
			running = append(running, fmt.Sprintf("model-%d-0", dimension))
		}
		running = append(running, "outside-matrix")

		b.Run(fmt.Sprintf("Combinations_%d", combinations), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkSolveResult = solver.Solve("target", running)
			}
		})
	}
}

var benchmarkEvict []string

// BenchmarkMatrixSwapPath measures one full scheduler swap decision:
// EvictionFor followed by OnSwapStart with the same running set, mirroring
// how the FIFO scheduler drives the planner.
func BenchmarkMatrixSwapPath(b *testing.B) {
	const setCount = 8
	matrix, models := benchmarkMultiSetMatrix(setCount, 10)
	if err := config.ValidateMatrix(matrix, models); err != nil {
		b.Fatal(err)
	}
	swapper := &matrixSwapper{
		solver: newMatrixSolver(matrix.Program(), matrix.ResolvedEvictCosts()),
		logger: logmon.NewWriter(io.Discard),
	}
	// Rotate the running set so each EvictionFor sees a fresh picture (cache
	// miss) while OnSwapStart repeats it (cache hit), mirroring the
	// scheduler: the running set changes between swaps, not within one.
	runSets := [][]string{
		{"a-3-1", "b-3-2", "a-0-1", "b-7-4", "outside-matrix"},
		{"a-3-3", "b-3-1", "a-1-2", "b-6-0", "outside-matrix"},
		{"a-3-0", "b-3-4", "a-2-3", "b-5-1", "outside-matrix"},
		{"a-3-2", "b-3-0", "a-4-4", "b-4-2", "outside-matrix"},
	}

	b.ReportAllocs()
	i := 0
	for b.Loop() {
		running := runSets[i%len(runSets)]
		i++
		benchmarkEvict = swapper.EvictionFor("target-3", running)
		swapper.OnSwapStart("target-3", running)
	}
}

// benchmarkMultiSetMatrix builds a matrix of setCount independent sets, each
// "target-N & (10 a-models) & (10 b-models)". Eviction costs use three
// distinct values: a-models cost 2, b-models cost 5, the rest default to 1.
func benchmarkMultiSetMatrix(setCount, choices int) (*config.MatrixConfig, map[string]config.ModelConfig) {
	models := map[string]config.ModelConfig{"outside-matrix": {}}
	evictCosts := make(map[string]int, 2*setCount*choices)
	sets := make(config.OrderedSets, 0, setCount)
	for s := range setCount {
		target := fmt.Sprintf("target-%d", s)
		models[target] = config.ModelConfig{}
		groupA := make([]string, 0, choices)
		groupB := make([]string, 0, choices)
		for c := range choices {
			a := fmt.Sprintf("a-%d-%d", s, c)
			bModel := fmt.Sprintf("b-%d-%d", s, c)
			models[a] = config.ModelConfig{}
			models[bModel] = config.ModelConfig{}
			evictCosts[a] = 2
			evictCosts[bModel] = 5
			groupA = append(groupA, a)
			groupB = append(groupB, bModel)
		}
		sets = append(sets, config.SetEntry{
			Name: fmt.Sprintf("set-%d", s),
			DSL: fmt.Sprintf("%s & (%s) & (%s)",
				target, strings.Join(groupA, " | "), strings.Join(groupB, " | ")),
		})
	}
	return &config.MatrixConfig{EvictCosts: evictCosts, Sets: sets}, models
}

func benchmarkCompiledMatrix(b *testing.B, dimensions, choices int) *matrixSolver {
	b.Helper()
	groups := make([]string, dimensions)
	models := map[string]config.ModelConfig{"target": {}}
	for dimension := range dimensions {
		alternatives := make([]string, choices)
		for choice := range choices {
			name := fmt.Sprintf("model-%d-%d", dimension, choice)
			alternatives[choice] = name
			models[name] = config.ModelConfig{}
		}
		groups[dimension] = "(" + strings.Join(alternatives, " | ") + ")"
	}

	matrix := &config.MatrixConfig{
		Sets: config.OrderedSets{{
			Name: "benchmark",
			DSL:  "target & " + strings.Join(groups, " & "),
		}},
	}
	if err := config.ValidateMatrix(matrix, models); err != nil {
		b.Fatal(err)
	}
	return newMatrixSolver(matrix.Program(), nil)
}

func benchmarkIntPow(base, exponent int) int {
	result := 1
	for range exponent {
		result *= base
	}
	return result
}
