package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHelpAgentConfig_DefaultsToEnabled(t *testing.T) {
	config, err := LoadConfigFromReader(strings.NewReader(`
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`))
	require.NoError(t, err)
	assert.False(t, config.HelpAgent.Disable)
}

func TestHelpAgentConfig_Disable(t *testing.T) {
	config, err := LoadConfigFromReader(strings.NewReader(`
helpagent:
  disable: true
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`))
	require.NoError(t, err)
	assert.True(t, config.HelpAgent.Disable)
}
