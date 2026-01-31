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

func TestConfig_APIKeys_Invalid(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expectedErr string
	}{
		{
			name:        "empty string",
			content:     `apiKeys: [""]`,
			expectedErr: "empty api key found in apiKeys",
		},
		{
			name:        "blank spaces only",
			content:     `apiKeys: ["   "]`,
			expectedErr: "api key cannot contain spaces: `   `",
		},
		{
			name:        "contains leading space",
			content:     `apiKeys: [" key123"]`,
			expectedErr: "api key cannot contain spaces: ` key123`",
		},
		{
			name:        "contains trailing space",
			content:     `apiKeys: ["key123 "]`,
			expectedErr: "api key cannot contain spaces: `key123 `",
		},
		{
			name:        "contains middle space",
			content:     `apiKeys: ["key 123"]`,
			expectedErr: "api key cannot contain spaces: `key 123`",
		},
		{
			name:        "empty in list with valid keys",
			content:     `apiKeys: ["valid-key", "", "another-key"]`,
			expectedErr: "empty api key found in apiKeys",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(tt.content))
			if assert.Error(t, err) {
				assert.Equal(t, tt.expectedErr, err.Error())
			}
		})
	}
}

func TestConfig_APIKeys_EnvMacros(t *testing.T) {
	t.Run("env substitution in apiKeys", func(t *testing.T) {
		t.Setenv("TEST_API_KEY", "secret-key-123")

		content := `apiKeys: ["${env.TEST_API_KEY}"]`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, []string{"secret-key-123"}, config.RequiredAPIKeys)
	})

	t.Run("multiple env substitutions in apiKeys", func(t *testing.T) {
		t.Setenv("TEST_API_KEY_1", "key-one")
		t.Setenv("TEST_API_KEY_2", "key-two")

		content := `apiKeys: ["${env.TEST_API_KEY_1}", "${env.TEST_API_KEY_2}", "static-key"]`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, []string{"key-one", "key-two", "static-key"}, config.RequiredAPIKeys)
	})

	t.Run("missing env var in apiKeys", func(t *testing.T) {
		content := `apiKeys: ["${env.NONEXISTENT_API_KEY}"]`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		assert.Error(t, err)
		// With string-level env substitution, error only includes var name
		assert.Contains(t, err.Error(), "NONEXISTENT_API_KEY")
	})

	t.Run("env substitution results in empty key", func(t *testing.T) {
		t.Setenv("TEST_EMPTY_KEY", "")

		content := `apiKeys: ["${env.TEST_EMPTY_KEY}"]`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		assert.Error(t, err)
		assert.Equal(t, "empty api key found in apiKeys", err.Error())
	})
}

func TestConfig_EnvMacros(t *testing.T) {
	t.Run("basic env substitution in cmd", func(t *testing.T) {
		t.Setenv("TEST_MODEL_PATH", "/opt/models")

		content := `
models:
  test:
    cmd: "${env.TEST_MODEL_PATH}/llama-server"
    proxy: "http://localhost:8080"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "/opt/models/llama-server", config.Models["test"].Cmd)
	})

	t.Run("env substitution in multiple fields", func(t *testing.T) {
		t.Setenv("TEST_HOST", "myserver")
		t.Setenv("TEST_PORT", "9999")

		content := `
models:
  test:
    cmd: "server --host ${env.TEST_HOST}"
    proxy: "http://${env.TEST_HOST}:${env.TEST_PORT}"
    checkEndpoint: "http://${env.TEST_HOST}/health"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "server --host myserver", config.Models["test"].Cmd)
		assert.Equal(t, "http://myserver:9999", config.Models["test"].Proxy)
		assert.Equal(t, "http://myserver/health", config.Models["test"].CheckEndpoint)
	})

	t.Run("env in global macro value", func(t *testing.T) {
		t.Setenv("TEST_BASE_PATH", "/usr/local")

		content := `
macros:
  SERVER_PATH: "${env.TEST_BASE_PATH}/bin/server"
models:
  test:
    cmd: "${SERVER_PATH} --port 8080"
    proxy: "http://localhost:8080"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "/usr/local/bin/server --port 8080", config.Models["test"].Cmd)
	})

	t.Run("env in model-level macro value", func(t *testing.T) {
		t.Setenv("TEST_MODEL_DIR", "/models/llama")

		content := `
models:
  test:
    macros:
      MODEL_FILE: "${env.TEST_MODEL_DIR}/model.gguf"
    cmd: "server --model ${MODEL_FILE}"
    proxy: "http://localhost:8080"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "server --model /models/llama/model.gguf", config.Models["test"].Cmd)
	})

	t.Run("env in metadata", func(t *testing.T) {
		t.Setenv("TEST_API_KEY", "secret123")

		content := `
models:
  test:
    cmd: "server"
    proxy: "http://localhost:8080"
    metadata:
      api_key: "${env.TEST_API_KEY}"
      nested:
        key: "${env.TEST_API_KEY}"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "secret123", config.Models["test"].Metadata["api_key"])
		nested := config.Models["test"].Metadata["nested"].(map[string]any)
		assert.Equal(t, "secret123", nested["key"])
	})

	t.Run("env in filters.stripParams", func(t *testing.T) {
		t.Setenv("TEST_STRIP_PARAMS", "temperature,top_p")

		content := `
models:
  test:
    cmd: "server"
    proxy: "http://localhost:8080"
    filters:
      stripParams: "${env.TEST_STRIP_PARAMS}"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "temperature,top_p", config.Models["test"].Filters.StripParams)
	})

	t.Run("env in cmdStop", func(t *testing.T) {
		t.Setenv("TEST_KILL_SIGNAL", "SIGTERM")

		content := `
models:
  test:
    cmd: "server --port ${PORT}"
    cmdStop: "kill -${env.TEST_KILL_SIGNAL} ${PID}"
    proxy: "http://localhost:${PORT}"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Contains(t, config.Models["test"].CmdStop, "-SIGTERM")
	})

	t.Run("missing env var returns error", func(t *testing.T) {
		content := `
models:
  test:
    cmd: "${env.UNDEFINED_VAR_12345}/server"
    proxy: "http://localhost:8080"
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "UNDEFINED_VAR_12345")
			assert.Contains(t, err.Error(), "not set")
		}
	})

	t.Run("missing env var in global macro", func(t *testing.T) {
		content := `
macros:
  PATH: "${env.UNDEFINED_GLOBAL_VAR}"
models:
  test:
    cmd: "server"
    proxy: "http://localhost:8080"
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "UNDEFINED_GLOBAL_VAR")
			assert.Contains(t, err.Error(), "not set")
		}
	})

	t.Run("missing env var in model macro", func(t *testing.T) {
		content := `
models:
  test:
    macros:
      MY_PATH: "${env.UNDEFINED_MODEL_VAR}"
    cmd: "server"
    proxy: "http://localhost:8080"
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "UNDEFINED_MODEL_VAR")
			assert.Contains(t, err.Error(), "not set")
		}
	})

	t.Run("missing env var in metadata", func(t *testing.T) {
		content := `
models:
  test:
    cmd: "server"
    proxy: "http://localhost:8080"
    metadata:
      key: "${env.UNDEFINED_META_VAR}"
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "UNDEFINED_META_VAR")
			assert.Contains(t, err.Error(), "not set")
		}
	})

	t.Run("env combined with regular macros", func(t *testing.T) {
		t.Setenv("TEST_ROOT", "/data")

		content := `
macros:
  MODEL_BASE: "${env.TEST_ROOT}/models"
models:
  test:
    cmd: "server --model ${MODEL_BASE}/${MODEL_ID}.gguf"
    proxy: "http://localhost:8080"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "server --model /data/models/test.gguf", config.Models["test"].Cmd)
	})

	t.Run("multiple env vars in same string", func(t *testing.T) {
		t.Setenv("TEST_USER", "admin")
		t.Setenv("TEST_PASS", "secret")

		content := `
models:
  test:
    cmd: "server --auth ${env.TEST_USER}:${env.TEST_PASS}"
    proxy: "http://localhost:8080"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "server --auth admin:secret", config.Models["test"].Cmd)
	})

	t.Run("env value with newline is rejected", func(t *testing.T) {
		t.Setenv("TEST_MULTILINE", "line1\nline2")

		content := `
models:
  test:
    cmd: "server --config ${env.TEST_MULTILINE}"
    proxy: "http://localhost:8080"
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "TEST_MULTILINE")
			assert.Contains(t, err.Error(), "newlines")
		}
	})

	t.Run("env value with carriage return is rejected", func(t *testing.T) {
		t.Setenv("TEST_CR", "line1\rline2")

		content := `
models:
  test:
    cmd: "server --config ${env.TEST_CR}"
    proxy: "http://localhost:8080"
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "TEST_CR")
			assert.Contains(t, err.Error(), "newlines")
		}
	})

	t.Run("env value with quotes is escaped for YAML", func(t *testing.T) {
		t.Setenv("TEST_QUOTED", `value with "quotes"`)

		content := `
models:
  test:
    cmd: "server --arg \"${env.TEST_QUOTED}\""
    proxy: "http://localhost:8080"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		// Quotes are escaped before YAML parsing, then YAML unescapes them
		// Final result preserves the original value with quotes
		assert.Contains(t, config.Models["test"].Cmd, `"quotes"`)
	})

	t.Run("env value with backslash is escaped for YAML", func(t *testing.T) {
		t.Setenv("TEST_BACKSLASH", `path\to\file`)

		content := `
models:
  test:
    cmd: "server --path \"${env.TEST_BACKSLASH}\""
    proxy: "http://localhost:8080"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		// Backslashes are escaped before YAML parsing, then YAML unescapes them
		// Final result preserves the original value with backslashes
		assert.Contains(t, config.Models["test"].Cmd, `path\to\file`)
	})
}

func TestConfig_PeerApiKey_EnvMacros(t *testing.T) {
	t.Run("env substitution in peer apiKey", func(t *testing.T) {
		t.Setenv("TEST_PEER_API_KEY", "sk-peer-secret-123")

		content := `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    apiKey: "${env.TEST_PEER_API_KEY}"
    models:
      - llama-3.1-8b
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "sk-peer-secret-123", config.Peers["openrouter"].ApiKey)
	})

	t.Run("missing env var in peer apiKey", func(t *testing.T) {
		content := `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    apiKey: "${env.NONEXISTENT_PEER_KEY}"
    models:
      - llama-3.1-8b
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		assert.Error(t, err)
		// With string-level env substitution, error only includes var name
		assert.Contains(t, err.Error(), "NONEXISTENT_PEER_KEY")
	})

	t.Run("static apiKey unchanged", func(t *testing.T) {
		content := `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    apiKey: sk-static-key
    models:
      - llama-3.1-8b
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "sk-static-key", config.Peers["openrouter"].ApiKey)
	})

	t.Run("multiple peers with env apiKeys", func(t *testing.T) {
		t.Setenv("TEST_PEER_KEY_1", "key-one")
		t.Setenv("TEST_PEER_KEY_2", "key-two")

		content := `
peers:
  peer1:
    proxy: https://peer1.example.com
    apiKey: "${env.TEST_PEER_KEY_1}"
    models:
      - model-a
  peer2:
    proxy: https://peer2.example.com
    apiKey: "${env.TEST_PEER_KEY_2}"
    models:
      - model-b
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "key-one", config.Peers["peer1"].ApiKey)
		assert.Equal(t, "key-two", config.Peers["peer2"].ApiKey)
	})

	t.Run("global macro substitution in peer apiKey", func(t *testing.T) {
		content := `
macros:
  API_KEY: sk-from-global-macro
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    apiKey: "${API_KEY}"
    models:
      - llama-3.1-8b
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "sk-from-global-macro", config.Peers["openrouter"].ApiKey)
	})

	t.Run("global macro in peer filters.stripParams", func(t *testing.T) {
		content := `
macros:
  STRIP_LIST: "temperature, top_p"
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    models:
      - llama-3.1-8b
    filters:
      stripParams: "${STRIP_LIST}"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "temperature, top_p", config.Peers["openrouter"].Filters.StripParams)
	})

	t.Run("global macro in peer filters.setParams", func(t *testing.T) {
		content := `
macros:
  MAX_TOKENS: 4096
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    models:
      - llama-3.1-8b
    filters:
      setParams:
        max_tokens: "${MAX_TOKENS}"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, 4096, config.Peers["openrouter"].Filters.SetParams["max_tokens"])
	})

	t.Run("env macro in peer filters.setParams", func(t *testing.T) {
		t.Setenv("TEST_RETENTION_POLICY", "deny")

		content := `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    models:
      - llama-3.1-8b
    filters:
      setParams:
        data_collection: "${env.TEST_RETENTION_POLICY}"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "deny", config.Peers["openrouter"].Filters.SetParams["data_collection"])
	})

	t.Run("env macro in peer filters.stripParams", func(t *testing.T) {
		t.Setenv("TEST_STRIP_PARAMS", "frequency_penalty, presence_penalty")

		content := `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    models:
      - llama-3.1-8b
    filters:
      stripParams: "${env.TEST_STRIP_PARAMS}"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, "frequency_penalty, presence_penalty", config.Peers["openrouter"].Filters.StripParams)
	})

	t.Run("unknown macro in peer apiKey fails", func(t *testing.T) {
		content := `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    apiKey: "${UNDEFINED_MACRO}"
    models:
      - llama-3.1-8b
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "peers.openrouter.apiKey")
		assert.Contains(t, err.Error(), "unknown macro")
	})

	t.Run("unknown macro in peer filters.setParams fails", func(t *testing.T) {
		content := `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    models:
      - llama-3.1-8b
    filters:
      setParams:
        value: "${UNDEFINED_MACRO}"
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "peers.openrouter.filters.setParams")
		assert.Contains(t, err.Error(), "unknown macro")
	})

	t.Run("env macros in comments are ignored", func(t *testing.T) {
		content := `
# apiKeys:
#   - "${env.COMMENTED_OUT_KEY_1}"
#   - "${env.COMMENTED_OUT_KEY_2}"
models:
  test:
    cmd: "server"
    proxy: "http://localhost:8080"
`
		// These env vars are NOT set, but should not cause an error
		// because they only appear in comment lines
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Empty(t, config.RequiredAPIKeys)
	})

	t.Run("env macros in comments ignored while active ones resolve", func(t *testing.T) {
		t.Setenv("TEST_ACTIVE_KEY", "active-key-value")

		content := `
# apiKeys: ["${env.COMMENTED_OUT_KEY}"]
apiKeys: ["${env.TEST_ACTIVE_KEY}"]
models:
  test:
    cmd: "server"
    proxy: "http://localhost:8080"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, []string{"active-key-value"}, config.RequiredAPIKeys)
	})

	t.Run("env macros in indented comments are ignored", func(t *testing.T) {
		content := `
models:
  test:
    cmd: |
      server
      --port 8080
    proxy: "http://localhost:8080"
    # metadata:
    #   api_key: "${env.SOME_UNSET_KEY}"
`
		_, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
	})

	t.Run("env macros in inline comments are ignored", func(t *testing.T) {
		t.Setenv("TEST_INLINE_KEY", "real-value")

		content := `
apiKeys: ["${env.TEST_INLINE_KEY}"] # TODO: add ${env.FUTURE_KEY} later
models:
  test:
    cmd: "server"
    proxy: "http://localhost:8080"
`
		config, err := LoadConfigFromReader(strings.NewReader(content))
		assert.NoError(t, err)
		assert.Equal(t, []string{"real-value"}, config.RequiredAPIKeys)
	})

}
