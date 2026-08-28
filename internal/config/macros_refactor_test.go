package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_MacrosAcrossConfiguration(t *testing.T) {
	content := `
macros:
  GLOBAL_TTL: 120
  MODEL_TTL: 45
  SESSION_HEADER: X-Macro-Session
  LOADING: true
  MODEL_NAME: model1

globalTTL: "${GLOBAL_TTL}"
sendLoadingState: "${LOADING}"
ui:
  activity:
    session_id: ["${SESSION_HEADER}"]
hooks:
  on_startup:
    preload: ["${MODEL_NAME}"]

models:
  model1:
    macros:
      MODEL_TTL: 30
      CONNECT_TIMEOUT: 12
    cmd: server --port ${PORT}
    ttl: "${MODEL_TTL}"
    timeouts:
      connect: "${CONNECT_TIMEOUT}"
    env:
      - "MODEL=${MODEL_ID}"
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)

	assert.Equal(t, 120, config.GlobalTTL)
	assert.True(t, config.SendLoadingState)
	assert.Equal(t, []string{"X-Macro-Session"}, config.UI.Activity.SessionID)
	assert.Equal(t, []string{"model1"}, config.Hooks.OnStartup.Preload)

	model := config.Models["model1"]
	assert.Equal(t, 30, model.UnloadAfter)
	assert.Equal(t, 12, model.Timeouts.Connect)
	assert.Equal(t, []string{"MODEL=model1"}, model.Env)
}

func TestConfig_MacroMapKeyScope(t *testing.T) {
	t.Run("ordinary keys remain literal", func(t *testing.T) {
		content := `
macros:
  KEY: expanded
models:
  model1:
    cmd: server
    proxy: http://localhost:8080
    metadata:
      "${KEY}": value
`

		config, err := LoadConfigFromReader(strings.NewReader(content))
		require.NoError(t, err)
		assert.Equal(t, "value", config.Models["model1"].Metadata["${KEY}"])
		assert.NotContains(t, config.Models["model1"].Metadata, "expanded")
	})

	t.Run("setParamsByID key collisions fail", func(t *testing.T) {
		content := `
macros:
  FIRST: shared
  SECOND: shared
models:
  model1:
    cmd: server
    proxy: http://localhost:8080
    filters:
      setParamsByID:
        "${FIRST}":
          temperature: 0.1
        "${SECOND}":
          temperature: 0.2
`

		_, err := LoadConfigFromReader(strings.NewReader(content))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resolve to the same value")
	})
}

func TestConfig_MacroForwardReferenceRejected(t *testing.T) {
	content := `
macros:
  FIRST: "value-${SECOND}"
  SECOND: later
models:
  model1:
    cmd: echo ${FIRST}
    proxy: http://localhost:8080
`

	_, err := LoadConfigFromReader(strings.NewReader(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown macro '${SECOND}'")
}

func TestConfig_RuntimeMacros(t *testing.T) {
	t.Run("PORT applies throughout its model", func(t *testing.T) {
		content := `
startPort: 6100
models:
  model1:
    cmd: server --port ${PORT}
    proxy: http://localhost:${PORT}
    checkEndpoint: /health/${PORT}
    aliases: ["port-${PORT}"]
    metadata:
      port: "${PORT}"
`

		config, err := LoadConfigFromReader(strings.NewReader(content))
		require.NoError(t, err)

		model := config.Models["model1"]
		assert.Equal(t, "server --port 6100", model.Cmd)
		assert.Equal(t, "http://localhost:6100", model.Proxy)
		assert.Equal(t, "/health/6100", model.CheckEndpoint)
		assert.Equal(t, []string{"port-6100"}, model.Aliases)
		assert.Equal(t, 6100, model.Metadata["port"])
	})

	t.Run("PID remains deferred in cmdStop", func(t *testing.T) {
		content := `
models:
  model1:
    cmd: server
    cmdStop: kill ${PID}
    proxy: http://localhost:8080
`

		config, err := LoadConfigFromReader(strings.NewReader(content))
		require.NoError(t, err)
		assert.Equal(t, "kill ${PID}", config.Models["model1"].CmdStop)
	})

	t.Run("PID is rejected outside cmdStop", func(t *testing.T) {
		content := `
models:
  model1:
    cmd: server
    proxy: http://localhost:8080
    metadata:
      pid: "${PID}"
`

		_, err := LoadConfigFromReader(strings.NewReader(content))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown macro '${PID}'")
	})
}
