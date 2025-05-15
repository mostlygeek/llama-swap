package proxy

import (
	"os"
	"path/filepath"
	"strings"
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
		StartPort: 5800,
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
			"model4": {
				Cmd:           "path/to/cmd --arg1 one",
				Proxy:         "http://localhost:8082",
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
		assert.Equal(t, "model model1 requires a proxy value when not using automatic ${PORT}", err.Error())
	})
}
