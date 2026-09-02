package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateMatrix_UndefinedVarInteraction(t *testing.T) {
	models := makeModels("a", "b")
	matrix := MatrixConfig{
		Var:  map[string]string{"alias": "a"},
		Sets: OrderedSets{{Name: "main", DSL: "alias & +undefined"}},
	}
	require.NoError(t, ValidateMatrix(&matrix, models))
	orphans, mode := matrix.Program().SynthesizedUndefined()
	require.Equal(t, "synthesized", mode)
	require.Equal(t, []string{"b"}, orphans)
}

func TestValidateMatrix_UndefinedTokenizerHostileModel(t *testing.T) {
	models := makeModels("a", "qwen3:32b")
	matrix := MatrixConfig{Sets: OrderedSets{{Name: "main", DSL: "a & +undefined"}}}
	require.NoError(t, ValidateMatrix(&matrix, models))
	result := matrix.Program().Solve("qwen3:32b", nil, nil)
	require.Equal(t, "main", result.SetName)
	require.Equal(t, []string{"a", "qwen3:32b"}, result.TargetSet)
}

func TestValidateMatrix_UndefinedEvictCosts(t *testing.T) {
	models := makeModels("a", "b", "c")
	matrix := MatrixConfig{
		EvictCosts: map[string]int{"b": 7},
		Sets:       OrderedSets{{Name: "main", DSL: "a & +undefined"}},
	}
	require.NoError(t, ValidateMatrix(&matrix, models))
	costs := matrix.ResolvedEvictCosts()
	require.Equal(t, 7, costs["b"])
	require.NotContains(t, costs, "c")
}

func TestValidateMatrix_UndefinedShadowing(t *testing.T) {
	models := makeModels("a", "b")
	matrix := MatrixConfig{Sets: OrderedSets{
		{Name: "undefined", DSL: "a"},
		{Name: "scratch", DSL: "+undefined"},
	}}
	require.NoError(t, ValidateMatrix(&matrix, models))
	_, mode := matrix.Program().SynthesizedUndefined()
	require.Equal(t, "user-defined", mode)
	require.Equal(t, "undefined", matrix.Program().Solve("a", nil, nil).SetName)
}
