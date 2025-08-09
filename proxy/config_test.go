package proxy

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

models:
  model1:
    cmd: |
      ${svr-path} ${argTwo}
      # the automatic ${PORT} is replaced
      ${autoPort}
      ${argOne}
      --arg3 three
    cmdStop: |
      /path/to/stop.sh --port ${PORT} ${argTwo}
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	sanitizedCmd, err := SanitizeCommand(config.Models["model1"].Cmd)
	assert.NoError(t, err)
	assert.Equal(t, "path/to/server --arg2 --port 9990 --arg1 --arg3 three", strings.Join(sanitizedCmd, " "))

	sanitizedCmdStop, err := SanitizeCommand(config.Models["model1"].CmdStop)
	assert.NoError(t, err)
	assert.Equal(t, "/path/to/stop.sh --port 9990 --arg2", strings.Join(sanitizedCmdStop, " "))
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
    proxy: "http://localhost:${unknownMacro}"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(tt.content))
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "unknown macro '${unknownMacro}' found in model1."+tt.field)
			//t.Log(err)
		})
	}
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

func TestConfig_HooksOnStartup(t *testing.T) {
	/* Placeholder to test
	- that real model names are resolved in hooks
	- everything that is not in models: is removed
	*/

}
