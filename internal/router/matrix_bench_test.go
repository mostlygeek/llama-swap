package router

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
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
