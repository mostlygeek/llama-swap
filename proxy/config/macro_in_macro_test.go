package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test macro-in-macro basic substitution
func TestConfig_MacroInMacroBasic(t *testing.T) {
	content := `
startPort: 10000
macros:
  "A": "value-A"
  "B": "prefix-${A}-suffix"

models:
  test:
    cmd: echo ${B}
    proxy: http://localhost:8080
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	assert.Equal(t, "echo prefix-value-A-suffix", config.Models["test"].Cmd)
}

// Test LIFO substitution order with 3+ macro levels
func TestConfig_MacroInMacroLIFOOrder(t *testing.T) {
	content := `
startPort: 10000
macros:
  "base": "/models"
  "path": "${base}/llama"
  "full": "${path}/model.gguf"

models:
  test:
    cmd: load ${full}
    proxy: http://localhost:8080
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	assert.Equal(t, "load /models/llama/model.gguf", config.Models["test"].Cmd)
}

// Test MODEL_ID in global macro used by model
func TestConfig_ModelIdInGlobalMacro(t *testing.T) {
	content := `
startPort: 10000
macros:
  "podman-llama": "podman run --name ${MODEL_ID} ghcr.io/ggml-org/llama.cpp:server-cuda"

models:
  my-model:
    cmd: ${podman-llama} -m model.gguf
    proxy: http://localhost:8080
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	assert.Equal(t, "podman run --name my-model ghcr.io/ggml-org/llama.cpp:server-cuda -m model.gguf", config.Models["my-model"].Cmd)
}

// Test model macro overrides global macro in substitution
func TestConfig_ModelMacroOverridesGlobal(t *testing.T) {
	content := `
startPort: 10000
macros:
  "tag": "global"
  "msg": "value-${tag}"

models:
  test:
    macros:
      "tag": "model-level"
    cmd: echo ${msg}
    proxy: http://localhost:8080
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	assert.Equal(t, "echo value-model-level", config.Models["test"].Cmd)
}

// Test self-reference detection error
func TestConfig_SelfReferenceDetection(t *testing.T) {
	content := `
startPort: 10000
macros:
  "recursive": "value-${recursive}"

models:
  test:
    cmd: echo ${recursive}
    proxy: http://localhost:8080
`

	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "recursive")
	assert.Contains(t, err.Error(), "self-reference")
}

// Test macro substitution in name and description fields
func TestConfig_MacroInNameAndDescription(t *testing.T) {
	content := `
startPort: 10000
macros:
  "VARIANT": "Q4_K_M"
  "FAMILY": "llama"

models:
  my-model:
    cmd: echo ok
    proxy: http://localhost:8080
    name: "${FAMILY} ${VARIANT}"
    description: "A ${FAMILY} model in ${VARIANT} format"
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	assert.Equal(t, "llama Q4_K_M", config.Models["my-model"].Name)
	assert.Equal(t, "A llama model in Q4_K_M format", config.Models["my-model"].Description)
}

// Test MODEL_ID macro in name and description fields
func TestConfig_ModelIDInNameAndDescription(t *testing.T) {
	content := `
startPort: 10000
models:
  llama-3b:
    cmd: echo ok
    proxy: http://localhost:8080
    name: "Model: ${MODEL_ID}"
    description: "Running ${MODEL_ID}"
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	assert.Equal(t, "Model: llama-3b", config.Models["llama-3b"].Name)
	assert.Equal(t, "Running llama-3b", config.Models["llama-3b"].Description)
}

// Test unknown macro in name or description returns an error
func TestConfig_UnknownMacroInNameDescription(t *testing.T) {
	content := `
startPort: 10000
models:
  test:
    cmd: echo ok
    proxy: http://localhost:8080
    name: "Model ${UNDEFINED}"
`

	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "UNDEFINED")
}

// Test undefined macro reference error
func TestConfig_UndefinedMacroReference(t *testing.T) {
	content := `
startPort: 10000
macros:
  "A": "value-${UNDEFINED}"

models:
  test:
    cmd: echo ${A}
    proxy: http://localhost:8080
`

	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "UNDEFINED")
}
