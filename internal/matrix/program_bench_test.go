package matrix

import (
	"fmt"
	"testing"
)

var (
	benchmarkProgram  *Program
	benchmarkDecision Decision
)

// BenchmarkProgram_SolveMultiSet measures Solve against several independent
// sets where only one contains the target. Each set is
// "target-N & (10 a-models) & (10 b-models)" with three distinct eviction
// cost values (a=2, b=5, default 1).
func BenchmarkProgram_SolveMultiSet(b *testing.B) {
	for _, setCount := range []int{1, 8, 32} {
		definitions, known, costs := multiSetDefinitions(setCount, 10)
		program, err := Compile(definitions, func(name string) (string, bool) {
			return name, known[name]
		})
		if err != nil {
			b.Fatal(err)
		}
		q := setCount / 2
		target := fmt.Sprintf("target-%d", q)
		running := []string{
			fmt.Sprintf("a-%d-1", q), fmt.Sprintf("b-%d-2", q),
			"a-0-0", "b-0-1", "outside",
		}

		b.Run(fmt.Sprintf("Sets_%d", setCount), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkDecision = program.Solve(target, running, costs)
			}
		})
	}
}

func multiSetDefinitions(setCount, choices int) ([]Definition, map[string]bool, map[string]int) {
	known := map[string]bool{"outside": true}
	costs := make(map[string]int, 2*setCount*choices)
	definitions := make([]Definition, 0, setCount)
	for s := range setCount {
		target := fmt.Sprintf("target-%d", s)
		known[target] = true
		groupA := make([]string, 0, choices)
		groupB := make([]string, 0, choices)
		for c := range choices {
			a := fmt.Sprintf("a-%d-%d", s, c)
			bModel := fmt.Sprintf("b-%d-%d", s, c)
			known[a] = true
			known[bModel] = true
			costs[a] = 2
			costs[bModel] = 5
			groupA = append(groupA, a)
			groupB = append(groupB, bModel)
		}
		dsl := target + " & (" + joinOr(groupA) + ") & (" + joinOr(groupB) + ")"
		definitions = append(definitions, Definition{Name: fmt.Sprintf("set-%d", s), DSL: dsl})
	}
	return definitions, known, costs
}

func joinOr(models []string) string {
	out := models[0]
	for _, m := range models[1:] {
		out += " | " + m
	}
	return out
}

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
