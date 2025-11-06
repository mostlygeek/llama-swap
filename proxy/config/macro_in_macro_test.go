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
