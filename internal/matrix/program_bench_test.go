package matrix

import (
	"fmt"
	"testing"
)

var benchmarkProgram *Program

func BenchmarkMatrix_Compile(b *testing.B) {
	for _, dimensions := range []int{2, 3, 4, 5} {
		const choices = 10
		combinations := benchmarkIntPow(choices, dimensions)
		definition, models := productDefinition(dimensions, choices)
		known := make(map[string]bool, len(models))
		for _, model := range models {
			known[model] = true
		}

		b.Run(fmt.Sprintf("Combinations_%d", combinations), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				program, err := Compile([]Definition{definition}, func(name string) (string, bool) {
					return name, known[name]
				})
				if err != nil {
					b.Fatal(err)
				}
				benchmarkProgram = program
			}
		})
	}
}

func benchmarkIntPow(base, exponent int) int {
	result := 1
	for range exponent {
		result *= base
	}
	return result
}
