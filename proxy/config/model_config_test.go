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
