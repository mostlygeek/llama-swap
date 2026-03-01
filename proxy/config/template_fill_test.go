package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExpandVariants_BasicExpansion(t *testing.T) {
	content := `
models:
  Qwen3.5-35B:
    cmd: llama-server --port ${PORT} --model qwen.gguf --temp 0.8
    variants:
      thinking_normal:
        cmdAdd: --temp 1.0
      thinking_coding:
        cmdAdd: --temp 0.6
      nothinking_normal:
        cmdAdd: "--temp 1.0 --chat-template-kwargs '{\"enable_thinking\": false}'"
      nothinking_coding:
        cmdAdd: "--temp 0.6 --chat-template-kwargs '{\"enable_thinking\": false}'"
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	// Original model with variants should not exist
	_, exists := config.Models["Qwen3.5-35B"]
	assert.False(t, exists, "Original template model should not exist in expanded config")

	// All 4 variants should exist
	expectedVariants := []string{
		"Qwen3.5-35B-thinking_normal",
		"Qwen3.5-35B-thinking_coding",
		"Qwen3.5-35B-nothinking_normal",
		"Qwen3.5-35B-nothinking_coding",
	}

	for _, variantName := range expectedVariants {
		_, exists := config.Models[variantName]
		assert.True(t, exists, "Variant %s should exist", variantName)
	}
}

func TestExpandVariants_CommandOverride(t *testing.T) {
	content := `
models:
  test-model:
    cmd: server --port ${PORT} --temp 0.8 --ctx 4096
    variants:
      high_temp:
        cmdAdd: --temp 1.0
      low_ctx:
        cmdAdd: --ctx 2048
      combined:
        cmdAdd: --temp 0.5 --ctx 8192
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	// Check high_temp variant - temp should be overridden
	highTemp := config.Models["test-model-high_temp"]
	assert.Contains(t, highTemp.Cmd, "--temp 1.0")
	assert.NotContains(t, highTemp.Cmd, "--temp 0.8")

	// Check low_ctx variant - ctx should be overridden
	lowCtx := config.Models["test-model-low_ctx"]
	assert.Contains(t, lowCtx.Cmd, "--ctx 2048")
	assert.NotContains(t, lowCtx.Cmd, "--ctx 4096")

	// Check combined variant - both should be overridden
	combined := config.Models["test-model-combined"]
	assert.Contains(t, combined.Cmd, "--temp 0.5")
	assert.Contains(t, combined.Cmd, "--ctx 8192")
	assert.NotContains(t, combined.Cmd, "--temp 0.8")
	assert.NotContains(t, combined.Cmd, "--ctx 4096")
}

func TestExpandVariants_NewArguments(t *testing.T) {
	content := `
models:
  test-model:
    cmd: server --port ${PORT}
    variants:
      with_extra:
        cmdAdd: --new-flag value --another-flag
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	model := config.Models["test-model-with_extra"]
	assert.Contains(t, model.Cmd, "--new-flag value")
	assert.Contains(t, model.Cmd, "--another-flag")
}

func TestExpandVariants_InheritedProperties(t *testing.T) {
	content := `
models:
  test-model:
    cmd: server --port ${PORT}
    name: "Test Model"
    description: "Base description"
    env:
      - "VAR1=value1"
    aliases:
      - "base-alias"
    ttl: 60
    unlisted: true
    variants:
      v1:
        cmdAdd: --variant v1
      v2:
        cmdAdd: --variant v2
        name: "Variant 2"
        description: "Custom description"
        env:
          - "VAR2=value2"
        aliases:
          - "v2-alias"
        unlisted: false
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	// v1 should inherit most properties but not aliases
	v1 := config.Models["test-model-v1"]
	assert.Equal(t, "Test Model", v1.Name)
	assert.Equal(t, "Base description", v1.Description)
	assert.Contains(t, v1.Env, "VAR1=value1")
	assert.Nil(t, v1.Aliases) // variants don't inherit base aliases
	assert.Equal(t, 60, v1.UnloadAfter)
	assert.True(t, v1.Unlisted)

	// v2 should have overridden properties
	v2 := config.Models["test-model-v2"]
	assert.Equal(t, "Variant 2", v2.Name)
	assert.Equal(t, "Custom description", v2.Description)
	assert.Contains(t, v2.Env, "VAR1=value1")       // env is inherited
	assert.Contains(t, v2.Env, "VAR2=value2")       // additional env
	assert.NotContains(t, v2.Aliases, "base-alias") // base aliases not inherited
	assert.Contains(t, v2.Aliases, "v2-alias")      // variant's own alias
	assert.False(t, v2.Unlisted)
}

func TestExpandVariants_NoVariants(t *testing.T) {
	content := `
models:
  model1:
    cmd: server1 --port ${PORT}
  model2:
    cmd: server2 --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	_, exists := config.Models["model1"]
	assert.True(t, exists)
	_, exists = config.Models["model2"]
	assert.True(t, exists)

	assert.Equal(t, 2, len(config.Models))
}

func TestExpandVariants_MixedModels(t *testing.T) {
	content := `
models:
  regular-model:
    cmd: server1 --port ${PORT}
  template-model:
    cmd: server2 --port ${PORT}
    variants:
      v1:
        cmdAdd: --v1
      v2:
        cmdAdd: --v2
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	_, exists := config.Models["regular-model"]
	assert.True(t, exists)

	_, exists = config.Models["template-model"]
	assert.False(t, exists)

	_, exists = config.Models["template-model-v1"]
	assert.True(t, exists)
	_, exists = config.Models["template-model-v2"]
	assert.True(t, exists)

	assert.Equal(t, 3, len(config.Models))
}

func TestExpandVariants_MacrosWork(t *testing.T) {
	content := `
startPort: 9000
macros:
  base_server: server
models:
  test-model:
    cmd: ${base_server} --port ${PORT} --temp 0.8
    variants:
      v1:
        cmdAdd: --temp 1.0
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	v1 := config.Models["test-model-v1"]
	assert.Contains(t, v1.Cmd, "server")
	assert.Contains(t, v1.Cmd, "--port 9000")
	assert.Contains(t, v1.Cmd, "--temp 1.0")
	assert.NotContains(t, v1.Cmd, "--temp 0.8")
}

func TestExpandVariants_AliasesUnique(t *testing.T) {
	content := `
models:
  model1:
    cmd: server --port ${PORT}
    variants:
      v1:
        aliases:
          - "shared-alias"
  model2:
    cmd: server --port ${PORT}
    variants:
      v1:
        aliases:
          - "shared-alias"
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate alias")
}

func TestMergeCommands_Basic(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		add      string
		expected string
	}{
		{
			name:     "override single arg",
			base:     "server --temp 0.8 --ctx 4096",
			add:      "--temp 1.0",
			expected: "server --temp 1.0 --ctx 4096",
		},
		{
			name:     "add new arg",
			base:     "server --temp 0.8",
			add:      "--ctx 4096",
			expected: "server --temp 0.8 --ctx 4096",
		},
		{
			name:     "override and add",
			base:     "server --temp 0.8",
			add:      "--temp 1.0 --ctx 4096",
			expected: "server --temp 1.0 --ctx 4096",
		},
		{
			name:     "empty add",
			base:     "server --temp 0.8",
			add:      "",
			expected: "server --temp 0.8",
		},
		{
			name:     "empty base",
			base:     "",
			add:      "--temp 0.8",
			expected: "--temp 0.8",
		},
		{
			name:     "equals format override",
			base:     "server --temp=0.8 --ctx=4096",
			add:      "--temp=1.0",
			expected: "server --temp=1.0 --ctx=4096",
		},
		{
			name:     "short flag override",
			base:     "server -t 0.8 -c 4096",
			add:      "-t 1.0",
			expected: "server -t 1.0 -c 4096",
		},
		{
			name:     "quoted value",
			base:     "server --arg value",
			add:      `--extra '{"key": "value"}'`,
			expected: `server --arg value --extra '{"key": "value"}'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeCommands(tt.base, tt.add)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTokenizeCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple command",
			input:    "server --port 8080",
			expected: []string{"server", "--port", "8080"},
		},
		{
			name:     "quoted string",
			input:    `server --arg "hello world"`,
			expected: []string{"server", "--arg", `"hello world"`},
		},
		{
			name:     "single quoted string",
			input:    `server --arg '{"key": "value"}'`,
			expected: []string{"server", "--arg", `'{"key": "value"}'`},
		},
		{
			name:     "equals format",
			input:    "server --port=8080 --temp=0.8",
			expected: []string{"server", "--port=8080", "--temp=0.8"},
		},
		{
			name:     "multiline",
			input:    "server\n--port\n8080",
			expected: []string{"server", "--port", "8080"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tokenizeCommand(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExpandVariants_VariantsNotCopied(t *testing.T) {
	content := `
models:
  test-model:
    cmd: server --port ${PORT}
    variants:
      v1:
        cmdAdd: --v1
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	v1 := config.Models["test-model-v1"]
	assert.Nil(t, v1.Variants, "Expanded models should not have variants field")
}

func TestExpandVariants_GroupsWithVariants(t *testing.T) {
	content := `
models:
  template-model:
    cmd: server --port ${PORT}
    variants:
      v1:
        cmdAdd: --v1
      v2:
        cmdAdd: --v2

groups:
  mygroup:
    members:
      - template-model-v1
      - template-model-v2
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	group := config.Groups["mygroup"]
	assert.Contains(t, group.Members, "template-model-v1")
	assert.Contains(t, group.Members, "template-model-v2")
}
