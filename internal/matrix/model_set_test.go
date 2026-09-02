package matrix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgram_OrphansCompileIsolation(t *testing.T) {
	definitions := []Definition{
		{Name: "first", DSL: "+" + OrphanSetName},
		{Name: "second", DSL: "+" + OrphanSetName},
	}
	first := compileOrphansTestProgram(t, definitions, []string{"a"})
	require.Len(t, first.sets, 2, "automatic root must not be selectable")
	root := first.sets[0].root.ref
	require.Same(t, root, first.sets[1].root.ref, "references share one generated root")

	// Building another model list neither registers nor changes any named set.
	other := buildModelSet([]string{"b"})
	require.NotSame(t, root, other)
	other.children[0].name = "changed"
	require.Equal(t, []string{"a"}, first.Solve("a", nil, nil).TargetSet)

	// A reload must recompute membership, without modifying the old program.
	second := compileOrphansTestProgram(t, definitions, []string{"b"})
	require.NotSame(t, root, second.sets[0].root.ref)
	models, mode := first.SynthesizedOrphans()
	require.Equal(t, "synthesized", mode)
	require.Equal(t, []string{"a"}, models)
	models[0] = "changed"
	models, _ = first.SynthesizedOrphans()
	require.Equal(t, []string{"a"}, models, "reporting must not expose mutable storage")
	models, _ = second.SynthesizedOrphans()
	require.Equal(t, []string{"b"}, models)
}

func TestProgram_BuildModelSet(t *testing.T) {
	for _, tt := range []struct {
		name   string
		models []string
		want   [][]string
	}{
		{name: "empty"},
		{name: "single", models: []string{"a"}, want: [][]string{{"a"}}},
		{name: "sorted alternatives", models: []string{"z", "a"}, want: [][]string{{"a"}, {"z"}}},
		{name: "resolved IDs bypass tokenizer", models: []string{"qwen3:32b", "org/model"}, want: [][]string{{"org/model"}, {"qwen3:32b"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := append([]string(nil), tt.models...)
			root := buildModelSet(tt.models)
			require.Equal(t, original, tt.models, "builder must not mutate its input")

			var combinations [][]string
			for _, state := range newEvaluator(tt.models).evaluate(root) {
				combinations = append(combinations, flattenWitness(state.witness))
			}
			require.Equal(t, tt.want, combinations)

			program := &Program{sets: []compiledSet{{name: "generated", root: root}}}
			program.computeSupport()
			for _, model := range tt.models {
				require.Equal(t, "generated", program.Solve(model, nil, nil).SetName)
			}
			if len(tt.models) == 0 {
				require.Equal(t, nodeEmpty, root.kind)
				require.Empty(t, program.modelBits)
				require.Empty(t, program.Solve("unknown", nil, nil).SetName)
			}
		})
	}
}
