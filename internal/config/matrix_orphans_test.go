package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestValidateMatrix_OrphansVarInteraction(t *testing.T) {
	models := makeModels("a", "b")
	matrix := MatrixConfig{
		Var:  map[string]string{"alias": "a"},
		Sets: OrderedSets{{Name: "main", DSL: "alias & +auto:orphans"}},
	}
	require.NoError(t, ValidateMatrix(&matrix, models))
	orphans, mode := matrix.Program().SynthesizedOrphans()
	require.Equal(t, "synthesized", mode)
	require.Equal(t, []string{"b"}, orphans)
}

func TestValidateMatrix_OrphansTokenizerHostileModel(t *testing.T) {
	models := makeModels("a", "qwen3:32b")
	matrix := MatrixConfig{Sets: OrderedSets{{Name: "main", DSL: "a & +auto:orphans"}}}
	require.NoError(t, ValidateMatrix(&matrix, models))
	result := matrix.Program().Solve("qwen3:32b", nil, nil)
	require.Equal(t, "main", result.SetName)
	require.Equal(t, []string{"a", "qwen3:32b"}, result.TargetSet)
}

func TestValidateMatrix_OrphansEvictCosts(t *testing.T) {
	models := makeModels("a", "b", "c")
	matrix := MatrixConfig{
		EvictCosts: map[string]int{"b": 7},
		Sets:       OrderedSets{{Name: "main", DSL: "a & +auto:orphans"}},
	}
	require.NoError(t, ValidateMatrix(&matrix, models))
	costs := matrix.ResolvedEvictCosts()
	require.Equal(t, 7, costs["b"])
	require.NotContains(t, costs, "c")
}

func TestValidateMatrix_OrphansYAML(t *testing.T) {
	for _, tt := range []struct {
		name, input, mode, set string
	}{
		{"generated", "sets:\n  main: 'a & +auto:orphans'\n", "synthesized", "main"},
		{"shadowed", "sets:\n  auto:orphans: b\n  main: 'a & +auto:orphans'\n", "user-defined", "auto:orphans"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var matrix MatrixConfig
			require.NoError(t, yaml.Unmarshal([]byte(tt.input), &matrix))
			require.NoError(t, ValidateMatrix(&matrix, makeModels("a", "b")))
			_, mode := matrix.Program().SynthesizedOrphans()
			require.Equal(t, tt.mode, mode)
			require.Equal(t, tt.set, matrix.Program().Solve("b", nil, nil).SetName)
		})
	}
}

func TestValidateMatrix_OrphansShadowing(t *testing.T) {
	models := makeModels("a", "b")
	matrix := MatrixConfig{Sets: OrderedSets{
		{Name: "auto:orphans", DSL: "a"},
		{Name: "scratch", DSL: "+auto:orphans"},
	}}
	require.NoError(t, ValidateMatrix(&matrix, models))
	_, mode := matrix.Program().SynthesizedOrphans()
	require.Equal(t, "user-defined", mode)
	require.Equal(t, "auto:orphans", matrix.Program().Solve("a", nil, nil).SetName)
}
