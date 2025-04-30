package proxy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_Load(t *testing.T) {
	// Create a temporary YAML file for testing
	tempDir, err := os.MkdirTemp("", "test-config")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempFile := filepath.Join(tempDir, "config.yaml")
	content := `
models:
  model1:
    cmd: path/to/cmd --arg1 one
    proxy: "http://localhost:8080"
    aliases:
      - "m1"
      - "model-one"
    env:
      - "VAR1=value1"
      - "VAR2=value2"
    checkEndpoint: "/health"
  model2:
    cmd: path/to/cmd --arg1 one
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
`

	if err := os.WriteFile(tempFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temporary file: %v", err)
	}

	// Load the config and verify
	config, err := LoadConfig(tempFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	expected := &Config{
		Models: map[string]ModelConfig{
			"model1": {
				Cmd:           "path/to/cmd --arg1 one",
				Proxy:         "http://localhost:8080",
				Aliases:       []string{"m1", "model-one"},
				Env:           []string{"VAR1=value1", "VAR2=value2"},
				CheckEndpoint: "/health",
			},
			"model2": {
				Cmd:           "path/to/cmd --arg1 one",
				Proxy:         "http://localhost:8081",
				Aliases:       []string{"m2"},
				Env:           nil,
				CheckEndpoint: "/",
			},
			"model3": {
				Cmd:           "path/to/cmd --arg1 one",
				Proxy:         "http://localhost:8081",
				Aliases:       []string{"mthree"},
				Env:           nil,
				CheckEndpoint: "/",
			},
		},
		HealthCheckTimeout: 15,
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
		},
	}

	assert.Equal(t, expected, config)

	realname, found := config.RealModelName("m1")
	assert.True(t, found)
	assert.Equal(t, "model1", realname)
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

func TestConfig_SanitizeCommand(t *testing.T) {

	// Test a command with spaces and newlines
	args, err := SanitizeCommand(`python model1.py \
    -a "double quotes" \
    --arg2 'single quotes'
	-s
	--arg3 123 \
	--arg4 '"string in string"'
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
