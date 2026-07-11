package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_ModelCapabilities_YAMLAnchors(t *testing.T) {
	content := `
defs:
  vlm-capabilities: &vlm-capabilities
    in:
      - text
      - image
    out: [text]
    tools: true
  llm-capabilities: &llm-capabilities
    in: [text]
    out: [text]
    tools: true
models:
  GLM-4.7-Flash-Q8_0-ik:
    capabilities:
      <<: [*llm-capabilities]
      context: 202752
    cmd: |
      ./llama-server
      # .... some other parameters using macros
      --port ${PORT}
      --model ./bartowski/zai-org_GLM-4.7-Flash-GGUF/zai-org_GLM-4.7-Flash-Q8_0.gguf
      --ubatch-size 4096
      --batch-size 4096
      --ctx-size 202752
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	if assert.NoError(t, err) {
		capabilities := config.Models["GLM-4.7-Flash-Q8_0-ik"].Capabilities
		assert.Equal(t, []string{"text"}, capabilities.In)
		assert.Equal(t, []string{"text"}, capabilities.Out)
		assert.True(t, capabilities.Tools)
		assert.Equal(t, 202752, capabilities.Context)
	}
}

func TestConfig_ModelCapabilities_YAMLAnchorsWithMacros(t *testing.T) {
	content := `
macros:
  default_ctx: 98304
defs:
  llm-capabilities: &llm-capabilities
    in: [text]
    out: [text]
    tools: true
    context: ${default_ctx}
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
    macros:
      default_ctx: 4096
    capabilities:
      <<: [*llm-capabilities]
  model2:
    cmd: path/to/cmd --port ${PORT}
    macros:
      default_ctx: 8192
    capabilities:
      <<: [*llm-capabilities]
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	if assert.NoError(t, err) {
		for modelID, expectedContext := range map[string]int{"model1": 4096, "model2": 8192} {
			capabilities := config.Models[modelID].Capabilities
			assert.Equal(t, []string{"text"}, capabilities.In)
			assert.Equal(t, []string{"text"}, capabilities.Out)
			assert.True(t, capabilities.Tools)
			assert.Equal(t, expectedContext, capabilities.Context)
		}
	}
}
