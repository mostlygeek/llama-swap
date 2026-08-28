package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_MacroNameValidation(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expectedErr string
	}{
		{
			name: "invalid characters",
			content: `
macros:
  bad.name: value
`,
			expectedErr: "contains invalid characters",
		},
		{
			name: "name exceeds maximum length",
			content: fmt.Sprintf(`
macros:
  %s: value
`, strings.Repeat("a", 64)),
			expectedErr: "exceeds maximum length",
		},
		{
			name: "model macro named PID",
			content: `
models:
  model1:
    macros:
      PID: 1234
`,
			expectedErr: "model model1: macro name 'PID' is reserved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(tt.content))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestConfig_ModelMacroDoesNotLeakToOtherModels(t *testing.T) {
	content := `
models:
  owner:
    macros:
      LOCAL: owner-value
    cmd: echo ${LOCAL}
    proxy: http://localhost:8080
  other:
    cmd: echo ${LOCAL}
    proxy: http://localhost:8081
`

	_, err := LoadConfigFromReader(strings.NewReader(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown macro '${LOCAL}' found in other.cmd")
}

func TestConfig_ModelMacrosExpandMaterializedYAMLAnchors(t *testing.T) {
	content := `
defs:
  model: &model
    cmd: echo ${LOCAL}
    proxy: http://localhost:8080
    env: ["MODEL_VALUE=${LOCAL}"]
    metadata:
      value: "${LOCAL}"

models:
  first:
    <<: *model
    macros:
      LOCAL: first-value
  second:
    <<: *model
    macros:
      LOCAL: second-value
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)

	for modelID, expected := range map[string]string{
		"first":  "first-value",
		"second": "second-value",
	} {
		model := config.Models[modelID]
		assert.Equal(t, "echo "+expected, model.Cmd)
		assert.Equal(t, []string{"MODEL_VALUE=" + expected}, model.Env)
		assert.Equal(t, expected, model.Metadata["value"])
	}
}

func TestConfig_MacroResolvedStartPort(t *testing.T) {
	content := `
macros:
  FIRST_PORT: 6200
startPort: "${FIRST_PORT}"
models:
  model1:
    cmd: server --port ${PORT}
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, 6200, config.StartPort)
	assert.Equal(t, "server --port 6200", config.Models["model1"].Cmd)
	assert.Equal(t, "http://localhost:6200", config.Models["model1"].Proxy)
}

func TestConfig_RuntimeMacrosIntroducedByRegularMacros(t *testing.T) {
	t.Run("PID remains deferred after regular expansion", func(t *testing.T) {
		content := `
macros:
  STOP_COMMAND: kill ${PID}
models:
  model1:
    cmd: server
    cmdStop: "${STOP_COMMAND}"
    proxy: http://localhost:8080
`

		config, err := LoadConfigFromReader(strings.NewReader(content))
		require.NoError(t, err)
		assert.Equal(t, "kill ${PID}", config.Models["model1"].CmdStop)
	})

	t.Run("PORT expands in setParamsByID keys", func(t *testing.T) {
		content := `
startPort: 6300
models:
  model1:
    cmd: server --port ${PORT}
    filters:
      setParamsByID:
        "model1:${PORT}":
          temperature: 0.5
`

		config, err := LoadConfigFromReader(strings.NewReader(content))
		require.NoError(t, err)

		model := config.Models["model1"]
		assert.Contains(t, model.Filters.SetParamsByID, "model1:6300")
		assert.Contains(t, model.Aliases, "model1:6300")
	})
}
