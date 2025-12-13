package proxy

import (
	"testing"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
)

func TestModelNeedsRestart(t *testing.T) {
	tests := []struct {
		name     string
		old      config.ModelConfig
		new      config.ModelConfig
		expected bool
	}{
		{
			name:     "identical configs",
			old:      config.ModelConfig{Cmd: "echo hello", Proxy: "http://localhost:8080"},
			new:      config.ModelConfig{Cmd: "echo hello", Proxy: "http://localhost:8080"},
			expected: false,
		},
		{
			name:     "cmd changed",
			old:      config.ModelConfig{Cmd: "echo hello", Proxy: "http://localhost:8080"},
			new:      config.ModelConfig{Cmd: "echo world", Proxy: "http://localhost:8080"},
			expected: true,
		},
		{
			name:     "proxy changed",
			old:      config.ModelConfig{Cmd: "echo hello", Proxy: "http://localhost:8080"},
			new:      config.ModelConfig{Cmd: "echo hello", Proxy: "http://localhost:9090"},
			expected: true,
		},
		{
			name:     "cmdStop changed",
			old:      config.ModelConfig{Cmd: "echo hello", CmdStop: "kill"},
			new:      config.ModelConfig{Cmd: "echo hello", CmdStop: "pkill"},
			expected: true,
		},
		{
			name:     "env changed",
			old:      config.ModelConfig{Cmd: "echo hello", Env: []string{"FOO=bar"}},
			new:      config.ModelConfig{Cmd: "echo hello", Env: []string{"FOO=baz"}},
			expected: true,
		},
		{
			name:     "checkEndpoint changed",
			old:      config.ModelConfig{Cmd: "echo hello", CheckEndpoint: "/health"},
			new:      config.ModelConfig{Cmd: "echo hello", CheckEndpoint: "/ready"},
			expected: true,
		},
		{
			name:     "ttl changed - no restart needed",
			old:      config.ModelConfig{Cmd: "echo hello", UnloadAfter: 60},
			new:      config.ModelConfig{Cmd: "echo hello", UnloadAfter: 120},
			expected: false,
		},
		{
			name:     "aliases changed - no restart needed",
			old:      config.ModelConfig{Cmd: "echo hello", Aliases: []string{"a"}},
			new:      config.ModelConfig{Cmd: "echo hello", Aliases: []string{"b"}},
			expected: false,
		},
		{
			name:     "concurrencyLimit changed",
			old:      config.ModelConfig{Cmd: "echo hello", ConcurrencyLimit: 5},
			new:      config.ModelConfig{Cmd: "echo hello", ConcurrencyLimit: 10},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := modelNeedsRestart(tt.old, tt.new)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldRestartModel(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name          string
		old           config.ModelConfig
		new           config.ModelConfig
		globalRestart bool
		expected      bool
	}{
		{
			name:          "no change, global false",
			old:           config.ModelConfig{Cmd: "echo hello"},
			new:           config.ModelConfig{Cmd: "echo hello"},
			globalRestart: false,
			expected:      false,
		},
		{
			name:          "changed, global false",
			old:           config.ModelConfig{Cmd: "echo hello"},
			new:           config.ModelConfig{Cmd: "echo world"},
			globalRestart: false,
			expected:      false,
		},
		{
			name:          "changed, global true",
			old:           config.ModelConfig{Cmd: "echo hello"},
			new:           config.ModelConfig{Cmd: "echo world"},
			globalRestart: true,
			expected:      true,
		},
		{
			name:          "changed, global false, model forceRestart true",
			old:           config.ModelConfig{Cmd: "echo hello"},
			new:           config.ModelConfig{Cmd: "echo world", ForceRestart: boolPtr(true)},
			globalRestart: false,
			expected:      true,
		},
		{
			name:          "changed, global true, model forceRestart false",
			old:           config.ModelConfig{Cmd: "echo hello"},
			new:           config.ModelConfig{Cmd: "echo world", ForceRestart: boolPtr(false)},
			globalRestart: true,
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldRestartModel(tt.old, tt.new, tt.globalRestart)
			assert.Equal(t, tt.expected, result)
		})
	}
}
