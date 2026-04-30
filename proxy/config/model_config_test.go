package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_ModelConfigSanitizedCommand(t *testing.T) {
	config := &ModelConfig{
		Cmd: `python model1.py \
    --arg1 value1 \
    --arg2 value2`,
	}

	args, err := config.SanitizedCommand()
	assert.NoError(t, err)
	assert.Equal(t, []string{"python", "model1.py", "--arg1", "value1", "--arg2", "value2"}, args)
}

func TestModelConfig_ContextSize(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		expected int
	}{
		{"long flag", "llama-server --port 8080 --ctx-size 4096 --model foo.gguf", 4096},
		{"equals syntax", "llama-server --ctx-size=196608 --port 8080", 196608},
		{"short flag", "llama-server -p 8080 -c 8192 -m foo.gguf", 8192},
		{"no context size", "llama-server --port 8080 --model foo.gguf", 0},
		{"short flag non-numeric", "python -c 'print(1)' --ctx-size 2048", 2048},
		{"last occurrence wins", "llama-server --ctx-size 1024 --ctx-size 2048", 2048},
		{"empty cmd", "", 0},
		{"large context", "llama-server -c 131072", 131072},
		{"vllm max-model-len", "vllm serve model --max-model-len 32768 --port 8080", 32768},
		{"vllm max-model-len equals", "vllm serve model --max-model-len=65536", 65536},
		{"shell var not resolved", "llama-server --ctx-size $CTX_SIZE", 0},
		{"parallel divides context", "llama-server --ctx-size 128000 --parallel 4", 32000},
		{"parallel short flag", "llama-server -c 128000 -np 4", 32000},
		{"parallel equals syntax", "llama-server --ctx-size=64000 --parallel=2", 32000},
		{"parallel 1 no division", "llama-server --ctx-size 128000 --parallel 1", 128000},
		{"parallel without ctx", "llama-server --parallel 4", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &ModelConfig{Cmd: tt.cmd}
			assert.Equal(t, tt.expected, m.ContextSize())
		})
	}
}

func TestModelConfig_ContextSize_EnvMacro(t *testing.T) {
	t.Setenv("LLAMA_ARG_CTX_SIZE", "65536")
	configYaml := `
models:
  model1:
    cmd: llama-server --port ${PORT} --ctx-size ${env.LLAMA_ARG_CTX_SIZE}
`
	cfg, err := LoadConfigFromReader(strings.NewReader(configYaml))
	assert.NoError(t, err)
	model1 := cfg.Models["model1"]
	assert.Equal(t, 65536, model1.ContextSize())
}

func TestModelConfig_SupportsVision(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		expected bool
	}{
		{"with mmproj", "llama-server --port 8080 --mmproj vision.gguf --model foo.gguf", true},
		{"with mmproj equals", "llama-server --mmproj=vision.gguf --port 8080", true},
		{"without mmproj", "llama-server --port 8080 --model foo.gguf", false},
		{"empty cmd", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &ModelConfig{Cmd: tt.cmd}
			assert.Equal(t, tt.expected, m.SupportsVision())
		})
	}
}

func TestConfig_ModelFilters(t *testing.T) {
	content := `
macros:
  default_strip: "temperature, top_p"
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
    filters:
      # macros inserted and list is cleaned of duplicates and empty strings
      stripParams: "model, top_k, top_k, temperature, ${default_strip}, , ,"
  # check for strip_params (legacy field name) compatibility
  legacy:
    cmd: path/to/cmd --port ${PORT}
    filters:
      strip_params: "model, top_k, top_k, temperature, ${default_strip}, , ,"
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	for modelId, modelConfig := range config.Models {
		t.Run(fmt.Sprintf("Testing macros in filters for model %s", modelId), func(t *testing.T) {
			assert.Equal(t, "model, top_k, top_k, temperature, temperature, top_p, , ,", modelConfig.Filters.StripParams)
			sanitized, err := modelConfig.Filters.SanitizedStripParams()
			if assert.NoError(t, err) {
				// model has been removed
				// empty strings have been removed
				// duplicates have been removed
				assert.Equal(t, []string{"temperature", "top_k", "top_p"}, sanitized)
			}
		})
	}
}

func TestConfig_ModelSendLoadingState(t *testing.T) {
	content := `
sendLoadingState: true
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
    sendLoadingState: false
  model2:
    cmd: path/to/cmd --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	assert.True(t, config.SendLoadingState)
	if assert.NotNil(t, config.Models["model1"].SendLoadingState) {
		assert.False(t, *config.Models["model1"].SendLoadingState)
	}
	if assert.NotNil(t, config.Models["model2"].SendLoadingState) {
		assert.True(t, *config.Models["model2"].SendLoadingState)
	}
}

func TestConfig_SetParamsByIDAutoAlias(t *testing.T) {
	content := `
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
    filters:
      setParamsByID:
        "${MODEL_ID}:high":
          reasoning_effort: high
        "${MODEL_ID}:low":
          reasoning_effort: low
`
	cfg, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	// Keys (other than the model's own ID) should be registered as aliases
	realName, found := cfg.RealModelName("model1:high")
	assert.True(t, found, "model1:high should be an auto-registered alias")
	assert.Equal(t, "model1", realName)

	realName, found = cfg.RealModelName("model1:low")
	assert.True(t, found, "model1:low should be an auto-registered alias")
	assert.Equal(t, "model1", realName)

	// Auto-aliases should also appear in modelConfig.Aliases
	aliases := cfg.Models["model1"].Aliases
	assert.Contains(t, aliases, "model1:high")
	assert.Contains(t, aliases, "model1:low")
}

func TestConfig_SetParamsByIDAutoAliasConflictWithModelID(t *testing.T) {
	content := `
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
    filters:
      setParamsByID:
        model2:
          reasoning_effort: high
  model2:
    cmd: path/to/cmd --port ${PORT}
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.ErrorContains(t, err, "conflicts with an existing model ID")
}

func TestConfig_SetParamsByIDAutoAliasConflictWithOtherModel(t *testing.T) {
	content := `
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
    filters:
      setParamsByID:
        "shared-alias":
          reasoning_effort: high
  model2:
    cmd: path/to/cmd --port ${PORT}
    filters:
      setParamsByID:
        "shared-alias":
          reasoning_effort: low
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.ErrorContains(t, err, "duplicate alias")
}

func TestConfig_ModelFiltersWithSetParams(t *testing.T) {
	content := `
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
    filters:
      stripParams: "top_k"
      setParams:
        temperature: 0.7
        top_p: 0.9
        stop:
          - "<|end|>"
          - "<|stop|>"
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	modelConfig := config.Models["model1"]

	// Check stripParams
	stripParams, err := modelConfig.Filters.SanitizedStripParams()
	assert.NoError(t, err)
	assert.Equal(t, []string{"top_k"}, stripParams)

	// Check setParams
	setParams, keys := modelConfig.Filters.SanitizedSetParams()
	assert.NotNil(t, setParams)
	assert.Equal(t, []string{"stop", "temperature", "top_p"}, keys)
	assert.Equal(t, 0.7, setParams["temperature"])
	assert.Equal(t, 0.9, setParams["top_p"])
}
