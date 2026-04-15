package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeModels(names ...string) map[string]ModelConfig {
	m := make(map[string]ModelConfig)
	for _, name := range names {
		m[name] = ModelConfig{Cmd: "echo " + name}
	}
	return m
}

func TestValidateMatrix_Basic(t *testing.T) {
	models := makeModels("gemma", "qwen", "mistral", "voxtral", "llama70B")

	matrix := MatrixConfig{
		Var: map[string]string{
			"g": "gemma",
			"q": "qwen",
			"m": "mistral",
			"v": "voxtral",
			"L": "llama70B",
		},
		EvictCosts: map[string]int{
			"L": 30,
			"v": 50,
		},
		Sets: OrderedSets{
			{Name: "standard", DSL: "(g | q | m) & v"},
			{Name: "full", DSL: "L"},
		},
	}

	expanded, err := ValidateMatrix(matrix, models)
	require.NoError(t, err)

	// standard expands to [gemma,voxtral], [qwen,voxtral], [mistral,voxtral]
	// full expands to [llama70B]
	assert.Len(t, expanded, 4)

	assert.Equal(t, "standard", expanded[0].SetName)
	assert.Equal(t, []string{"gemma", "voxtral"}, expanded[0].Models)

	assert.Equal(t, "standard", expanded[1].SetName)
	assert.Equal(t, []string{"qwen", "voxtral"}, expanded[1].Models)

	assert.Equal(t, "standard", expanded[2].SetName)
	assert.Equal(t, []string{"mistral", "voxtral"}, expanded[2].Models)

	assert.Equal(t, "full", expanded[3].SetName)
	assert.Equal(t, []string{"llama70B"}, expanded[3].Models)
}

func TestValidateMatrix_WithRef(t *testing.T) {
	models := makeModels("gemma", "qwen", "mistral", "voxtral", "reranker")

	matrix := MatrixConfig{
		Var: map[string]string{
			"g": "gemma",
			"q": "qwen",
			"m": "mistral",
			"v": "voxtral",
			"e": "reranker",
		},
		Sets: OrderedSets{
			{Name: "llms", DSL: "g | q | m"},
			{Name: "with_tts", DSL: "+llms & v"},
			{Name: "mega", DSL: "+with_tts & e"},
		},
	}

	expanded, err := ValidateMatrix(matrix, models)
	require.NoError(t, err)

	// llms: [gemma], [qwen], [mistral]
	// with_tts: [gemma,voxtral], [qwen,voxtral], [mistral,voxtral]
	// mega: [gemma,reranker,voxtral], [qwen,reranker,voxtral], [mistral,reranker,voxtral]
	assert.Len(t, expanded, 9)

	// Check mega entries
	megaEntries := filterBySetName(expanded, "mega")
	assert.Len(t, megaEntries, 3)
	assert.Equal(t, []string{"gemma", "reranker", "voxtral"}, megaEntries[0].Models)
}

func TestValidateMatrix_MapIDRequired(t *testing.T) {
	// DSL cannot use real model names directly — must use var IDs
	models := makeModels("gemma", "voxtral")

	matrix := MatrixConfig{
		Var: map[string]string{"g": "gemma"},
		Sets: OrderedSets{
			{Name: "combo", DSL: "g & voxtral"},
		},
	}

	_, err := ValidateMatrix(matrix, models)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown var ID")
}

func TestValidateMatrix_InvalidAliasKey(t *testing.T) {
	models := makeModels("gemma")

	tests := []struct {
		name   string
		alias  string
		errMsg string
	}{
		{"too long", "abcdefghi", "alphanumeric and 1-8 characters"},
		{"has underscore", "a_b", "alphanumeric and 1-8 characters"},
		{"has hyphen", "a-b", "alphanumeric and 1-8 characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matrix := MatrixConfig{
				Var:  map[string]string{tt.alias: "gemma"},
				Sets: OrderedSets{{Name: "s", DSL: tt.alias}},
			}
			_, err := ValidateMatrix(matrix, models)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestValidateMatrix_AliasReferencesUnknownModel(t *testing.T) {
	models := makeModels("gemma")

	matrix := MatrixConfig{
		Var:  map[string]string{"x": "nonexistent"},
		Sets: OrderedSets{{Name: "s", DSL: "x"}},
	}

	_, err := ValidateMatrix(matrix, models)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown model")
}

func TestValidateMatrix_EvictCostInvalid(t *testing.T) {
	models := makeModels("gemma")

	t.Run("zero cost", func(t *testing.T) {
		matrix := MatrixConfig{
			Var:        map[string]string{"g": "gemma"},
			EvictCosts: map[string]int{"g": 0},
			Sets:       OrderedSets{{Name: "s", DSL: "g"}},
		}
		_, err := ValidateMatrix(matrix, models)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "positive integer")
	})

	t.Run("negative cost", func(t *testing.T) {
		matrix := MatrixConfig{
			Var:        map[string]string{"g": "gemma"},
			EvictCosts: map[string]int{"g": -1},
			Sets:       OrderedSets{{Name: "s", DSL: "g"}},
		}
		_, err := ValidateMatrix(matrix, models)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "positive integer")
	})

	t.Run("unknown var ID in evict_costs", func(t *testing.T) {
		matrix := MatrixConfig{
			Var:        map[string]string{"g": "gemma"},
			EvictCosts: map[string]int{"unknown": 5},
			Sets:       OrderedSets{{Name: "s", DSL: "g"}},
		}
		_, err := ValidateMatrix(matrix, models)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown var ID")
	})
}

func TestValidateMatrix_CycleDetection(t *testing.T) {
	models := makeModels("gemma")

	matrix := MatrixConfig{
		Var: map[string]string{"g": "gemma"},
		Sets: OrderedSets{
			{Name: "a", DSL: "+b"},
			{Name: "b", DSL: "+a"},
		},
	}

	_, err := ValidateMatrix(matrix, models)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circular reference")
}

func TestValidateMatrix_UndefinedRefTarget(t *testing.T) {
	models := makeModels("gemma")

	matrix := MatrixConfig{
		Var: map[string]string{"g": "gemma"},
		Sets: OrderedSets{
			{Name: "a", DSL: "+nonexistent"},
		},
	}

	_, err := ValidateMatrix(matrix, models)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "references undefined set")
}

func TestValidateMatrix_NoSets(t *testing.T) {
	_, err := ValidateMatrix(MatrixConfig{}, makeModels("gemma"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one set")
}

func TestValidateMatrix_UnknownMapIDInDSL(t *testing.T) {
	models := makeModels("gemma")

	matrix := MatrixConfig{
		Var: map[string]string{"g": "gemma"},
		Sets: OrderedSets{
			{Name: "s", DSL: "g & nonexistent"},
		},
	}

	_, err := ValidateMatrix(matrix, models)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown var ID")
}

func TestValidateMatrix_ResolvedEvictCosts(t *testing.T) {
	mc := &MatrixConfig{
		Var: map[string]string{
			"g": "gemma",
			"L": "llama70B",
		},
		EvictCosts: map[string]int{
			"L": 30,
			"g": 5,
		},
	}

	costs := mc.ResolvedEvictCosts()
	assert.Equal(t, 30, costs["llama70B"])
	assert.Equal(t, 5, costs["gemma"])
}

func TestValidateMatrix_ConfigXOR(t *testing.T) {
	// groups and matrix both defined
	yaml := `
models:
  model1:
    cmd: echo model1
    proxy: http://localhost:8080
groups:
  group1:
    members:
      - model1
matrix:
  sets:
    s: "model1"
`
	_, err := LoadConfigFromReader(strings.NewReader(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot use both")
}

func TestValidateMatrix_ConfigMatrixOnly(t *testing.T) {
	yaml := `
models:
  gemma:
    cmd: echo gemma
    proxy: http://localhost:8080
  qwen:
    cmd: echo qwen
    proxy: http://localhost:8081
matrix:
  vars:
    g: gemma
    q: qwen
  sets:
    combo: "g | q"
`
	cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
	require.NoError(t, err)
	assert.NotNil(t, cfg.Matrix)
	assert.Len(t, cfg.ExpandedSets, 2)
	// Groups should be empty when matrix is used
	assert.Empty(t, cfg.Groups)
}

func filterBySetName(sets []ExpandedSet, name string) []ExpandedSet {
	var result []ExpandedSet
	for _, s := range sets {
		if s.SetName == name {
			result = append(result, s)
		}
	}
	return result
}
