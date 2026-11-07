package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hooksTestModels = `
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
    aliases: [default-chat]
  model2:
    cmd: path/to/cmd --port ${PORT}
`

func TestConfig_HooksOnUnloadRestore(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(hooksTestModels + `
hooks:
  on_unload:
    restore: [default-chat, " ", model2]
`))
	require.NoError(t, err)
	assert.Equal(t, []string{"model1", "model2"}, cfg.Hooks.OnUnload.Restore, "aliases resolve, blanks are dropped")
	assert.Equal(t, 30, cfg.Hooks.OnUnload.Delay, "delay defaults to 30")
}

func TestConfig_HooksOnUnloadRestore_Delay(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(hooksTestModels + `
hooks:
  on_unload:
    restore: [model1]
    delay: 120
`))
	require.NoError(t, err)
	assert.Equal(t, 120, cfg.Hooks.OnUnload.Delay)
}

func TestConfig_HooksOnUnloadRestore_UnknownModel(t *testing.T) {
	_, err := LoadConfigFromReader(strings.NewReader(hooksTestModels + `
hooks:
  on_unload:
    restore: [nope]
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `hooks.on_unload.restore references unknown model "nope"`)
}

func TestConfig_HooksOnUnloadRestore_NegativeDelay(t *testing.T) {
	_, err := LoadConfigFromReader(strings.NewReader(hooksTestModels + `
hooks:
  on_unload:
    restore: [model1]
    delay: -1
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hooks.on_unload.delay must be >= 0")
}

func TestConfig_HooksOnUnloadUnset(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(hooksTestModels))
	require.NoError(t, err)
	assert.Empty(t, cfg.Hooks.OnUnload.Restore)
	assert.Equal(t, 0, cfg.Hooks.OnUnload.Delay, "no default delay without restore models")
}
