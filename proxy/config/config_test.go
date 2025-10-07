package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_GroupMemberIsUnique(t *testing.T) {
	content := `
models:
  model1:
    cmd: path/to/cmd --arg1 one
    proxy: "http://localhost:8080"
  model2:
    cmd: path/to/cmd --arg1 one
    proxy: "http://localhost:8081"
    checkEndpoint: "/"
  model3:
    cmd: path/to/cmd --arg1 one
    proxy: "http://localhost:8081"
    checkEndpoint: "/"

healthCheckTimeout: 15
groups:
  group1:
    swap: true
    exclusive: false
    members: ["model2"]
  group2:
    swap: true
    exclusive: false
    members: ["model2"]
`
	// Load the config and verify
	_, err := LoadConfigFromReader(strings.NewReader(content))

	// a Contains as order of the map is not guaranteed
	assert.Contains(t, err.Error(), "model member model2 is used in multiple groups:")
}

func TestConfig_ModelAliasesAreUnique(t *testing.T) {
	content := `
models:
  model1:
    cmd: path/to/cmd --arg1 one
    proxy: "http://localhost:8080"
    aliases:
      - m1
  model2:
    cmd: path/to/cmd --arg1 one
    proxy: "http://localhost:8081"
    checkEndpoint: "/"
    aliases:
      - m1
      - m2
`
	// Load the config and verify
	_, err := LoadConfigFromReader(strings.NewReader(content))

	// this is a contains because it could be `model1` or `model2` depending on the order
	// go decided on the order of the map
	assert.Contains(t, err.Error(), "duplicate alias m1 found in model: model")
}

func TestConfig_FindConfig(t *testing.T) {

	// TODO?
	// make make this shared between the different tests
	config := &Config{
		Models: map[string]ModelConfig{
			"model1": {
				Cmd:           "python model1.py",
				Proxy:         "http://localhost:8080",
				Aliases:       []string{"m1", "model-one"},
				Env:           []string{"VAR1=value1", "VAR2=value2"},
				CheckEndpoint: "/health",
			},
			"model2": {
				Cmd:           "python model2.py",
				Proxy:         "http://localhost:8081",
				Aliases:       []string{"m2", "model-two"},
				Env:           []string{"VAR3=value3", "VAR4=value4"},
				CheckEndpoint: "/status",
			},
		},
		HealthCheckTimeout: 10,
		aliases: map[string]string{
			"m1":        "model1",
			"model-one": "model1",
			"m2":        "model2",
		},
	}

	// Test finding a model by its name
	modelConfig, modelId, found := config.FindConfig("model1")
	assert.True(t, found)
	assert.Equal(t, "model1", modelId)
	assert.Equal(t, config.Models["model1"], modelConfig)

	// Test finding a model by its alias
	modelConfig, modelId, found = config.FindConfig("m1")
	assert.True(t, found)
	assert.Equal(t, "model1", modelId)
	assert.Equal(t, config.Models["model1"], modelConfig)

	// Test finding a model that does not exist
	modelConfig, modelId, found = config.FindConfig("model3")
	assert.False(t, found)
	assert.Equal(t, "", modelId)
	assert.Equal(t, ModelConfig{}, modelConfig)
}

func TestConfig_AutomaticPortAssignments(t *testing.T) {

	t.Run("Default Port Ranges", func(t *testing.T) {
		content := ``
		config, err := LoadConfigFromReader(strings.NewReader(content))
		if !assert.NoError(t, err) {
			t.Fatalf("Failed to load config: %v", err)
		}

		assert.Equal(t, 5800, config.StartPort)
	})
	t.Run("User specific port ranges", func(t *testing.T) {
		content := `startPort: 1000`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		if !assert.NoError(t, err) {
			t.Fatalf("Failed to load config: %v", err)
		}

		assert.Equal(t, 1000, config.StartPort)
	})

	t.Run("Invalid start port", func(t *testing.T) {
		content := `startPort: abcd`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NotNil(t, err)
	})

	t.Run("start port must be greater than 1", func(t *testing.T) {
		content := `startPort: -99`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NotNil(t, err)
	})

	t.Run("Automatic port assignments", func(t *testing.T) {
		content := `
startPort: 5800
models:
  model1:
    cmd: svr --port ${PORT}
  model2:
    cmd: svr --port ${PORT}
    proxy: "http://172.11.22.33:${PORT}"
  model3:
    cmd: svr --port 1999
    proxy: "http://1.2.3.4:1999"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		if !assert.NoError(t, err) {
			t.Fatalf("Failed to load config: %v", err)
		}

		assert.Equal(t, 5800, config.StartPort)
		assert.Equal(t, "svr --port 5800", config.Models["model1"].Cmd)
		assert.Equal(t, "http://localhost:5800", config.Models["model1"].Proxy)

		assert.Equal(t, "svr --port 5801", config.Models["model2"].Cmd)
		assert.Equal(t, "http://172.11.22.33:5801", config.Models["model2"].Proxy)

		assert.Equal(t, "svr --port 1999", config.Models["model3"].Cmd)
		assert.Equal(t, "http://1.2.3.4:1999", config.Models["model3"].Proxy)

	})

	t.Run("Proxy value required if no ${PORT} in cmd", func(t *testing.T) {
		content := `
models:
  model1:
    cmd: svr --port 111
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		assert.Equal(t, "model model1: proxy uses ${PORT} but cmd does not - ${PORT} is only available when used in cmd", err.Error())
	})
}

func TestConfig_MacroReplacement(t *testing.T) {
	content := `
startPort: 9990
macros:
  svr-path: "path/to/server"
  argOne: "--arg1"
  argTwo: "--arg2"
  autoPort: "--port ${PORT}"
  overriddenByModelMacro: failed

models:
  model1:
    macros:
      overriddenByModelMacro: success
    cmd: |
      ${svr-path} ${argTwo}
      # the automatic ${PORT} is replaced
      ${autoPort}
      ${argOne}
      --arg3 three
      --overridden ${overriddenByModelMacro}
    cmdStop: |
      /path/to/stop.sh --port ${PORT} ${argTwo}
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	sanitizedCmd, err := SanitizeCommand(config.Models["model1"].Cmd)
	assert.NoError(t, err)
	assert.Equal(t, "path/to/server --arg2 --port 9990 --arg1 --arg3 three --overridden success", strings.Join(sanitizedCmd, " "))

	sanitizedCmdStop, err := SanitizeCommand(config.Models["model1"].CmdStop)
	assert.NoError(t, err)
	assert.Equal(t, "/path/to/stop.sh --port 9990 --arg2", strings.Join(sanitizedCmdStop, " "))
}

func TestConfig_MacroReservedNames(t *testing.T) {

	tests := []struct {
		name          string
		config        string
		expectedError string
	}{
		{
			name: "global macro named PORT",
			config: `
macros:
  PORT: "1111"
`,
			expectedError: "macro name 'PORT' is reserved",
		},
		{
			name: "global macro named MODEL_ID",
			config: `
macros:
  MODEL_ID: model1
`,
			expectedError: "macro name 'MODEL_ID' is reserved",
		},
		{
			name: "model macro named PORT",
			config: `
models:
  model1:
    macros:
      PORT: 1111
`,
			expectedError: "model model1: macro name 'PORT' is reserved",
		},

		{
			name: "model macro named MODEL_ID",
			config: `
models:
  model1:
    macros:
      MODEL_ID: model1
`,
			expectedError: "model model1: macro name 'MODEL_ID' is reserved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(tt.config))
			assert.NotNil(t, err)
			assert.Equal(t, tt.expectedError, err.Error())
		})
	}
}

func TestConfig_MacroErrorOnUnknownMacros(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		content string
	}{
		{
			name:  "unknown macro in cmd",
			field: "cmd",
			content: `
startPort: 9990
macros:
  svr-path: "path/to/server"
models:
  model1:
    cmd: |
      ${svr-path} --port ${PORT}
      ${unknownMacro}
`,
		},
		{
			name:  "unknown macro in cmdStop",
			field: "cmdStop",
			content: `
startPort: 9990
macros:
  svr-path: "path/to/server"
models:
  model1:
    cmd: "${svr-path} --port ${PORT}"
    cmdStop: "kill ${unknownMacro}"
`,
		},
		{
			name:  "unknown macro in proxy",
			field: "proxy",
			content: `
startPort: 9990
macros:
  svr-path: "path/to/server"
models:
  model1:
    cmd: "${svr-path} --port ${PORT}"
    proxy: "http://${unknownMacro}:${PORT}"
`,
		},
		{
			name:  "unknown macro in checkEndpoint",
			field: "checkEndpoint",
			content: `
startPort: 9990
macros:
  svr-path: "path/to/server"
models:
  model1:
    cmd: "${svr-path} --port ${PORT}"
    checkEndpoint: "http://localhost:${unknownMacro}/health"
`,
		},
		{
			name:  "unknown macro in filters.stripParams",
			field: "filters.stripParams",
			content: `
startPort: 9990
macros:
  svr-path: "path/to/server"
models:
  model1:
    cmd: "${svr-path} --port ${PORT}"
    filters:
      stripParams: "model,${unknownMacro}"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(tt.content))
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), "unknown macro '${unknownMacro}' found in model1."+tt.field)
			}
			//t.Log(err)
		})
	}
}
func TestStripComments(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no comments",
			input:    "echo hello\necho world",
			expected: "echo hello\necho world",
		},
		{
			name:     "single comment line",
			input:    "# this is a comment\necho hello",
			expected: "echo hello",
		},
		{
			name:     "multiple comment lines",
			input:    "# comment 1\necho hello\n# comment 2\necho world",
			expected: "echo hello\necho world",
		},
		{
			name:     "comment with spaces",
			input:    "   # indented comment\necho hello",
			expected: "echo hello",
		},
		{
			name:     "empty lines preserved",
			input:    "echo hello\n\necho world",
			expected: "echo hello\n\necho world",
		},
		{
			name:     "only comments",
			input:    "# comment 1\n# comment 2",
			expected: "",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripComments(tt.input)
			if result != tt.expected {
				t.Errorf("StripComments() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestConfig_MacroInCommentStrippedBeforeExpansion(t *testing.T) {
	// Test case that reproduces the original bug where a macro in a comment
	// would get expanded and cause the comment text to be included in the command
	content := `
startPort: 9990
macros:
  "latest-llama": >
    /user/llama.cpp/build/bin/llama-server
    --port ${PORT}

models:
  "test-model":
    cmd: |
      # ${latest-llama} is a macro that is defined above
      ${latest-llama}
      --model /path/to/model.gguf
      -ngl 99
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	// Get the sanitized command
	sanitizedCmd, err := SanitizeCommand(config.Models["test-model"].Cmd)
	assert.NoError(t, err)

	// Join the command for easier inspection
	cmdStr := strings.Join(sanitizedCmd, " ")

	// Verify that comment text is NOT present in the final command as separate arguments
	commentWords := []string{"is", "macro", "that", "defined", "above"}
	for _, word := range commentWords {
		found := slices.Contains(sanitizedCmd, word)
		assert.False(t, found, "Comment text '%s' should not be present as a separate argument in final command", word)
	}

	// Verify that the actual command components ARE present
	expectedParts := []string{
		"/user/llama.cpp/build/bin/llama-server",
		"--port",
		"9990",
		"--model",
		"/path/to/model.gguf",
		"-ngl",
		"99",
	}

	for _, part := range expectedParts {
		assert.Contains(t, cmdStr, part, "Expected command part '%s' not found in final command", part)
	}

	// Verify the server path appears exactly once (not duplicated due to macro expansion)
	serverPath := "/user/llama.cpp/build/bin/llama-server"
	count := strings.Count(cmdStr, serverPath)
	assert.Equal(t, 1, count, "Expected exactly 1 occurrence of server path, found %d", count)

	// Verify the expected final command structure
	expectedCmd := "/user/llama.cpp/build/bin/llama-server --port 9990 --model /path/to/model.gguf -ngl 99"
	assert.Equal(t, expectedCmd, cmdStr, "Final command does not match expected structure")
}

func TestConfig_MacroModelId(t *testing.T) {
	content := `
startPort: 9000
macros:
  "docker-llama": docker run --name ${MODEL_ID} -p ${PORT}:8080 docker_img
  "docker-stop": docker stop ${MODEL_ID}

models:
  model1:
    cmd: /path/to/server -p ${PORT} -hf ${MODEL_ID}

  model2:
    cmd: ${docker-llama}
    cmdStop: ${docker-stop}

  author/model:F16:
    cmd: /path/to/server -p ${PORT} -hf ${MODEL_ID}
    cmdStop: stop
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	sanitizedCmd, err := SanitizeCommand(config.Models["model1"].Cmd)
	assert.NoError(t, err)
	assert.Equal(t, "/path/to/server -p 9001 -hf model1", strings.Join(sanitizedCmd, " "))

	dockerStopMacro, found := config.Macros.Get("docker-stop")
	assert.True(t, found)
	assert.Equal(t, "docker stop ${MODEL_ID}", dockerStopMacro)

	sanitizedCmd2, err := SanitizeCommand(config.Models["model2"].Cmd)
	assert.NoError(t, err)
	assert.Equal(t, "docker run --name model2 -p 9002:8080 docker_img", strings.Join(sanitizedCmd2, " "))

	sanitizedCmdStop, err := SanitizeCommand(config.Models["model2"].CmdStop)
	assert.NoError(t, err)
	assert.Equal(t, "docker stop model2", strings.Join(sanitizedCmdStop, " "))

	sanitizedCmd3, err := SanitizeCommand(config.Models["author/model:F16"].Cmd)
	assert.NoError(t, err)
	assert.Equal(t, "/path/to/server -p 9000 -hf author/model:F16", strings.Join(sanitizedCmd3, " "))
}

func TestConfig_TypedMacrosInMetadata(t *testing.T) {
	content := `
startPort: 10000
macros:
  PORT_NUM: 10001
  TEMP: 0.7
  ENABLED: true
  NAME: "llama model"
  CTX: 16384

models:
  test-model:
    cmd: /path/to/server -p ${PORT}
    metadata:
      port: ${PORT_NUM}
      temperature: ${TEMP}
      enabled: ${ENABLED}
      model_name: ${NAME}
      context: ${CTX}
      note: "Running on port ${PORT_NUM} with temp ${TEMP} and context ${CTX}"
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	meta := config.Models["test-model"].Metadata
	assert.NotNil(t, meta)

	// Verify direct substitution preserves types
	assert.Equal(t, 10001, meta["port"])
	assert.Equal(t, 0.7, meta["temperature"])
	assert.Equal(t, true, meta["enabled"])
	assert.Equal(t, "llama model", meta["model_name"])
	assert.Equal(t, 16384, meta["context"])

	// Verify string interpolation converts to string
	assert.Equal(t, "Running on port 10001 with temp 0.7 and context 16384", meta["note"])
}

func TestConfig_NestedStructuresInMetadata(t *testing.T) {
	content := `
startPort: 10000
macros:
  TEMP: 0.7

models:
  test-model:
    cmd: /path/to/server -p ${PORT}
    metadata:
      config:
        port: ${PORT}
        temperature: ${TEMP}
      tags: ["model:${MODEL_ID}", "port:${PORT}"]
      nested:
        deep:
          value: ${TEMP}
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	meta := config.Models["test-model"].Metadata
	assert.NotNil(t, meta)

	// Verify nested objects
	configMap := meta["config"].(map[string]any)
	assert.Equal(t, 10000, configMap["port"])
	assert.Equal(t, 0.7, configMap["temperature"])

	// Verify arrays
	tags := meta["tags"].([]any)
	assert.Equal(t, "model:test-model", tags[0])
	assert.Equal(t, "port:10000", tags[1])

	// Verify deeply nested structures
	nested := meta["nested"].(map[string]any)
	deep := nested["deep"].(map[string]any)
	assert.Equal(t, 0.7, deep["value"])
}

func TestConfig_ModelLevelMacroPrecedenceInMetadata(t *testing.T) {
	content := `
startPort: 10000
macros:
  TEMP: 0.5
  GLOBAL_VAL: "global"

models:
  test-model:
    cmd: /path/to/server -p ${PORT}
    macros:
      TEMP: 0.9
      LOCAL_VAL: "local"
    metadata:
      temperature: ${TEMP}
      global: ${GLOBAL_VAL}
      local: ${LOCAL_VAL}
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	meta := config.Models["test-model"].Metadata
	assert.NotNil(t, meta)

	// Model-level macro should override global
	assert.Equal(t, 0.9, meta["temperature"])
	// Global macro should be accessible
	assert.Equal(t, "global", meta["global"])
	// Model-level macro should be accessible
	assert.Equal(t, "local", meta["local"])
}

func TestConfig_UnknownMacroInMetadata(t *testing.T) {
	content := `
startPort: 10000
models:
  test-model:
    cmd: /path/to/server -p ${PORT}
    metadata:
      value: ${UNKNOWN_MACRO}
`

	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "test-model")
	assert.Contains(t, err.Error(), "UNKNOWN_MACRO")
}

func TestConfig_InvalidMacroType(t *testing.T) {
	content := `
startPort: 10000
macros:
  INVALID:
    nested: value

models:
  test-model:
    cmd: /path/to/server -p ${PORT}
`

	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "INVALID")
	assert.Contains(t, err.Error(), "must be a scalar type")
}

func TestConfig_MacroTypeValidation(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		shouldErr bool
	}{
		{
			name: "string macro",
			yaml: `
startPort: 10000
macros:
  STR: "test"
models:
  test-model:
    cmd: /path/to/server -p ${PORT}
`,
			shouldErr: false,
		},
		{
			name: "int macro",
			yaml: `
startPort: 10000
macros:
  NUM: 42
models:
  test-model:
    cmd: /path/to/server -p ${PORT}
`,
			shouldErr: false,
		},
		{
			name: "float macro",
			yaml: `
startPort: 10000
macros:
  FLOAT: 3.14
models:
  test-model:
    cmd: /path/to/server -p ${PORT}
`,
			shouldErr: false,
		},
		{
			name: "bool macro",
			yaml: `
startPort: 10000
macros:
  BOOL: true
models:
  test-model:
    cmd: /path/to/server -p ${PORT}
`,
			shouldErr: false,
		},
		{
			name: "array macro (invalid)",
			yaml: `
startPort: 10000
macros:
  ARR: [1, 2, 3]
models:
  test-model:
    cmd: /path/to/server -p ${PORT}
`,
			shouldErr: true,
		},
		{
			name: "map macro (invalid)",
			yaml: `
startPort: 10000
macros:
  MAP:
    key: value
models:
  test-model:
    cmd: /path/to/server -p ${PORT}
`,
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(tt.yaml))
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
