//go:build !windows

package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_SanitizeCommand(t *testing.T) {
	// Test a command with spaces and newlines
	args, err := SanitizeCommand(`python model1.py \
		-a "double quotes" \
		--arg2 'single quotes'
		-s
		# comment 1
		--arg3 123 \

		  # comment 2
		--arg4 '"string in string"'


		# this will get stripped out as well as the white space above
		-c "'single quoted'"
		`)
	assert.NoError(t, err)
	assert.Equal(t, []string{
		"python", "model1.py",
		"-a", "double quotes",
		"--arg2", "single quotes",
		"-s",
		"--arg3", "123",
		"--arg4", `"string in string"`,
		"-c", `'single quoted'`,
	}, args)

	// Test an empty command
	args, err = SanitizeCommand("")
	assert.Error(t, err)
	assert.Nil(t, args)
}

// Test the default values are automatically set for global, model and group configurations
// after loading the configuration
func TestConfig_DefaultValuesPosix(t *testing.T) {
	content := `
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	assert.Equal(t, 120, config.HealthCheckTimeout)
	assert.Equal(t, 5800, config.StartPort)
	assert.Equal(t, "info", config.LogLevel)

	// Test default group exists
	defaultGroup, exists := config.Groups["(default)"]
	assert.True(t, exists, "default group should exist")
	if assert.NotNil(t, defaultGroup, "default group should not be nil") {
		assert.Equal(t, true, defaultGroup.Swap)
		assert.Equal(t, true, defaultGroup.Exclusive)
		assert.Equal(t, false, defaultGroup.Persistent)
		assert.Equal(t, []string{"model1"}, defaultGroup.Members)
	}

	model1, exists := config.Models["model1"]
	assert.True(t, exists, "model1 should exist")
	if assert.NotNil(t, model1, "model1 should not be nil") {
		assert.Equal(t, "path/to/cmd --port 5800", model1.Cmd) // has the port replaced
		assert.Equal(t, "", model1.CmdStop)
		assert.Equal(t, "http://localhost:5800", model1.Proxy)
		assert.Equal(t, "/health", model1.CheckEndpoint)
		assert.Equal(t, []string{}, model1.Aliases)
		assert.Equal(t, []string{}, model1.Env)
		assert.Equal(t, 0, model1.UnloadAfter)
		assert.Equal(t, false, model1.Unlisted)
		assert.Equal(t, "", model1.UseModelName)
		assert.Equal(t, 0, model1.ConcurrencyLimit)
	}

	// default empty filter exists
	assert.Equal(t, "", model1.Filters.StripParams)
}

func TestConfig_LoadPosix(t *testing.T) {
	// Create a temporary YAML file for testing
	tempDir, err := os.MkdirTemp("", "test-config")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempFile := filepath.Join(tempDir, "config.yaml")
	content := `
macros:
  svr-path: "path/to/server"
hooks:
  on_startup:
    preload: ["model1", "model2"]
models:
  model1:
    cmd: path/to/cmd --arg1 one
    proxy: "http://localhost:8080"
    name: "Model 1"
    description: "This is model 1"
    aliases:
      - "m1"
      - "model-one"
    env:
      - "VAR1=value1"
      - "VAR2=value2"
    checkEndpoint: "/health"
  model2:
    cmd: ${svr-path} --arg1 one
    proxy: "http://localhost:8081"
    aliases:
      - "m2"
    checkEndpoint: "/"
  model3:
    cmd: path/to/cmd --arg1 one
    proxy: "http://localhost:8081"
    aliases:
      - "mthree"
    checkEndpoint: "/"
  model4:
    cmd: path/to/cmd --arg1 one
    proxy: "http://localhost:8082"
    checkEndpoint: "/"

healthCheckTimeout: 15
profiles:
  test:
    - model1
    - model2
groups:
  group1:
    swap: true
    exclusive: false
    members: ["model2"]
  forever:
    exclusive: false
    persistent: true
    members:
      - "model4"
`

	if err := os.WriteFile(tempFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temporary file: %v", err)
	}

	// Load the config and verify
	config, err := LoadConfig(tempFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	expected := Config{
		LogLevel:  "info",
		StartPort: 5800,
		Macros: map[string]string{
			"svr-path": "path/to/server",
		},
		Hooks: HooksConfig{
			OnStartup: HookOnStartup{
				Preload: []string{"model1", "model2"},
			},
		},
		Models: map[string]ModelConfig{
			"model1": {
				Cmd:           "path/to/cmd --arg1 one",
				Proxy:         "http://localhost:8080",
				Aliases:       []string{"m1", "model-one"},
				Env:           []string{"VAR1=value1", "VAR2=value2"},
				CheckEndpoint: "/health",
				Name:          "Model 1",
				Description:   "This is model 1",
			},
			"model2": {
				Cmd:           "path/to/server --arg1 one",
				Proxy:         "http://localhost:8081",
				Aliases:       []string{"m2"},
				Env:           []string{},
				CheckEndpoint: "/",
			},
			"model3": {
				Cmd:           "path/to/cmd --arg1 one",
				Proxy:         "http://localhost:8081",
				Aliases:       []string{"mthree"},
				Env:           []string{},
				CheckEndpoint: "/",
			},
			"model4": {
				Cmd:           "path/to/cmd --arg1 one",
				Proxy:         "http://localhost:8082",
				CheckEndpoint: "/",
				Aliases:       []string{},
				Env:           []string{},
			},
		},
		HealthCheckTimeout: 15,
		MetricsMaxInMemory: 1000,
		Profiles: map[string][]string{
			"test": {"model1", "model2"},
		},
		aliases: map[string]string{
			"m1":        "model1",
			"model-one": "model1",
			"m2":        "model2",
			"mthree":    "model3",
		},
		Groups: map[string]GroupConfig{
			DEFAULT_GROUP_ID: {
				Swap:      true,
				Exclusive: true,
				Members:   []string{"model1", "model3"},
			},
			"group1": {
				Swap:      true,
				Exclusive: false,
				Members:   []string{"model2"},
			},
			"forever": {
				Swap:       true,
				Exclusive:  false,
				Persistent: true,
				Members:    []string{"model4"},
			},
		},
	}

	assert.Equal(t, expected, config)

	realname, found := config.RealModelName("m1")
	assert.True(t, found)
	assert.Equal(t, "model1", realname)
}
