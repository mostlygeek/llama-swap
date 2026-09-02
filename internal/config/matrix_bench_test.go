package config

import (
	"fmt"
	"strings"
	"testing"
)

var (
	benchmarkMatrixConfig *MatrixConfig
)

func BenchmarkMatrix_Validate(b *testing.B) {
	for _, dimensions := range []int{2, 3, 4, 5} {
		combinations := intPow(10, dimensions)
		dsl, models := benchmarkMatrixExpression(dimensions, 10)

		b.Run(fmt.Sprintf("Combinations_%d", combinations), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				matrix := &MatrixConfig{
					Sets: OrderedSets{{Name: "benchmark", DSL: dsl}},
				}
				if err := ValidateMatrix(matrix, models); err != nil {
					b.Fatal(err)
				}
				benchmarkMatrixConfig = matrix
			}
		})
	}
}

func benchmarkMatrixExpression(dimensions, choices int) (string, map[string]ModelConfig) {
	groups := make([]string, dimensions)
	models := make(map[string]ModelConfig, dimensions*choices)
	for dimension := range dimensions {
		alternatives := make([]string, choices)
		for choice := range choices {
			name := fmt.Sprintf("model-%d-%d", dimension, choice)
			alternatives[choice] = name
			models[name] = ModelConfig{}
		}
		groups[dimension] = "(" + strings.Join(alternatives, " | ") + ")"
	}
	return strings.Join(groups, " & "), models
}

func intPow(base, exponent int) int {
	result := 1
	for range exponent {
		result *= base
	}
	return result
}
