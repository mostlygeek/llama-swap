package config

import (
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
      strip_params: "model, top_k, ${default_strip}, , ,"
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	modelConfig, ok := config.Models["model1"]
	if !assert.True(t, ok) {
		t.FailNow()
	}

	// make sure `model` and enmpty strings are not in the list
	assert.Equal(t, "model, top_k, temperature, top_p, , ,", modelConfig.Filters.StripParams)
	sanitized, err := modelConfig.Filters.SanitizedStripParams()
	if assert.NoError(t, err) {
		assert.Equal(t, []string{"temperature", "top_k", "top_p"}, sanitized)
	}
}
