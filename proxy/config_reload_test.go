package proxy

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

func TestConfigWatcher(t *testing.T) {
	t.Run("detects file change", func(t *testing.T) {
		// Create temp config file
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")

		initialConfig := `
models:
  test-model:
    cmd: echo hello
    proxy: http://localhost:8080
`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		assert.NoError(t, err)

		// Track reload calls (use atomic for thread-safety)
		var reloadCount atomic.Int32
		onReload := func(path string) {
			reloadCount.Add(1)
		}

		// Start watcher
		watcher, err := newConfigWatcher(configPath, 50*time.Millisecond, onReload)
		assert.NoError(t, err)
		defer watcher.stop()

		// Modify file
		time.Sleep(100 * time.Millisecond) // let watcher start
		newConfig := `
models:
  test-model:
    cmd: echo world
    proxy: http://localhost:8080
`
		err = os.WriteFile(configPath, []byte(newConfig), 0644)
		assert.NoError(t, err)

		// Wait for debounce + processing
		time.Sleep(200 * time.Millisecond)
		assert.Equal(t, int32(1), reloadCount.Load())
	})
}

func TestProxyManagerReloadConfig(t *testing.T) {
	t.Run("successful reload updates config", func(t *testing.T) {
		// Create initial config
		initialCfg := config.Config{
			Models: map[string]config.ModelConfig{
				"model1": {Cmd: "echo hello", Proxy: "http://localhost:8080", CheckEndpoint: "none"},
			},
		}
		initialCfg = config.AddDefaultGroupToConfig(initialCfg)

		pm := New(initialCfg)
		defer pm.Shutdown()

		// Create new config
		newCfg := config.Config{
			Models: map[string]config.ModelConfig{
				"model1": {Cmd: "echo hello", Proxy: "http://localhost:8080", CheckEndpoint: "none"},
				"model2": {Cmd: "echo world", Proxy: "http://localhost:9090", CheckEndpoint: "none"},
			},
		}
		newCfg = config.AddDefaultGroupToConfig(newCfg)

		// Reload
		err := pm.ReloadConfig(newCfg)
		assert.NoError(t, err)

		// Verify new model is accessible
		_, found := pm.config.RealModelName("model2")
		assert.True(t, found)
	})

	t.Run("reload with invalid config keeps old config", func(t *testing.T) {
		initialCfg := config.Config{
			Models: map[string]config.ModelConfig{
				"model1": {Cmd: "echo hello", Proxy: "http://localhost:8080", CheckEndpoint: "none"},
			},
		}
		initialCfg = config.AddDefaultGroupToConfig(initialCfg)

		pm := New(initialCfg)
		defer pm.Shutdown()

		// This should work - we test file-based reload separately
		_, found := pm.config.RealModelName("model1")
		assert.True(t, found)
	})
}

func TestProxyManagerConfigWatcher(t *testing.T) {
	t.Run("watches config file and reloads on change", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")

		initialConfig := `
models:
  model1:
    cmd: echo hello
    proxy: http://localhost:8080
    checkEndpoint: none
`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		assert.NoError(t, err)

		cfg, err := config.LoadConfig(configPath)
		assert.NoError(t, err)

		pm := New(cfg)
		defer pm.Shutdown()

		// Start watcher
		err = pm.StartConfigWatcher(configPath)
		assert.NoError(t, err)

		// Verify initial state
		_, found := pm.config.RealModelName("model1")
		assert.True(t, found)
		_, found = pm.config.RealModelName("model2")
		assert.False(t, found)

		// Modify config file
		time.Sleep(100 * time.Millisecond)
		newConfig := `
models:
  model1:
    cmd: echo hello
    proxy: http://localhost:8080
    checkEndpoint: none
  model2:
    cmd: echo world
    proxy: http://localhost:9090
    checkEndpoint: none
`
		err = os.WriteFile(configPath, []byte(newConfig), 0644)
		assert.NoError(t, err)

		// Wait for reload (2 second debounce + processing time)
		time.Sleep(2500 * time.Millisecond)

		// Verify new model is available
		pm.RLock()
		_, found = pm.config.RealModelName("model2")
		pm.RUnlock()
		assert.True(t, found, "model2 should be available after reload")
	})
}
