package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturesYAML builds a store section; extra lines go under captures.
func capturesYAML(activityPath, capturesPath string, extra ...string) string {
	yaml := "store:\n  path: " + activityPath + "\n  captures:\n    path: " + capturesPath + "\n"
	for _, line := range extra {
		yaml += "    " + line + "\n"
	}
	return yaml
}

func TestConfig_StoreCaptures(t *testing.T) {
	t.Run("absent keeps the legacy in-memory behavior", func(t *testing.T) {
		cfg, err := LoadConfigFromReader(strings.NewReader(`
store:
  path: ./llama-swap.db
`))
		require.NoError(t, err)
		require.NotNil(t, cfg.Store)
		assert.Nil(t, cfg.Store.Captures, "no captures section must leave captures in memory")

		cfg, err = LoadConfigFromReader(strings.NewReader(`logLevel: info`))
		require.NoError(t, err)
		assert.Nil(t, cfg.Store, "no store section at all")
		assert.Equal(t, 5, cfg.CaptureBuffer, "the legacy default buffer must survive")
		assert.Equal(t, 1000, cfg.MetricsMaxInMemory)
	})

	t.Run("defaults", func(t *testing.T) {
		dir := t.TempDir()
		cfg, err := LoadConfigFromReader(strings.NewReader(
			capturesYAML(filepath.Join(dir, "activity.db"), filepath.Join(dir, "captures.db"))))
		require.NoError(t, err)
		require.NotNil(t, cfg.Store.Captures)
		assert.Equal(t, StoreCapturesDefaultMaxSizeMB, cfg.Store.Captures.MaxSizeMB)
	})

	t.Run("explicit values", func(t *testing.T) {
		dir := t.TempDir()
		cfg, err := LoadConfigFromReader(strings.NewReader(
			capturesYAML(filepath.Join(dir, "activity.db"), filepath.Join(dir, "captures.db"), "maxSizeMB: 2048")))
		require.NoError(t, err)
		require.NotNil(t, cfg.Store.Captures)
		assert.Equal(t, 2048, cfg.Store.Captures.MaxSizeMB)
	})

	t.Run("explicit null is not a section", func(t *testing.T) {
		cfg, err := LoadConfigFromReader(strings.NewReader(`
store:
  path: ./llama-swap.db
  captures:
`))
		require.NoError(t, err)
		assert.Nil(t, cfg.Store.Captures)
	})

	// captureBuffer: 0 counts too: "captures off" is a decision the new
	// section owns as well.
	t.Run("legacy settings are rejected alongside", func(t *testing.T) {
		for name, legacy := range map[string]string{
			"captureBuffer":      "captureBuffer: 20\n",
			"captureBuffer zero": "captureBuffer: 0\n",
			"metricsMaxInMemory": "metricsMaxInMemory: 500\n",
		} {
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				_, err := LoadConfigFromReader(strings.NewReader(legacy +
					capturesYAML(filepath.Join(dir, "activity.db"), filepath.Join(dir, "captures.db"))))
				require.Error(t, err)
				assert.Contains(t, err.Error(), "store.captures supersedes the legacy")
			})
		}
	})

	t.Run("legacy keys alone are fine", func(t *testing.T) {
		cfg, err := LoadConfigFromReader(strings.NewReader(`
captureBuffer: 20
metricsMaxInMemory: 500
`))
		require.NoError(t, err)
		assert.Equal(t, 20, cfg.CaptureBuffer)
		assert.Equal(t, 500, cfg.MetricsMaxInMemory)
	})

	t.Run("requires store.path", func(t *testing.T) {
		dir := t.TempDir()
		_, err := LoadConfigFromReader(strings.NewReader(`
store:
  captures:
    path: ` + filepath.Join(dir, "captures.db") + `
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "store.captures requires a non-empty store.path")
	})

	t.Run("empty captures path rejected", func(t *testing.T) {
		dir := t.TempDir()
		_, err := LoadConfigFromReader(strings.NewReader(
			capturesYAML(filepath.Join(dir, "activity.db"), "")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "store.captures.path must not be empty")
	})

	t.Run("unwritable captures directory rejected", func(t *testing.T) {
		dir := t.TempDir()
		_, err := LoadConfigFromReader(strings.NewReader(
			capturesYAML(filepath.Join(dir, "activity.db"), "/no/such/dir/captures.db")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "store.captures.path")
		assert.Contains(t, err.Error(), "not writable")
	})

	t.Run("sharing the activity database is rejected", func(t *testing.T) {
		shared := filepath.Join(t.TempDir(), "store.db")
		_, err := LoadConfigFromReader(strings.NewReader(capturesYAML(shared, shared)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "captures must use their own database file")
	})

	t.Run("uncleaned spellings of one file are rejected", func(t *testing.T) {
		dir := t.TempDir()
		_, err := LoadConfigFromReader(strings.NewReader(
			capturesYAML(filepath.Join(dir, "store.db"), dir+"/./store.db")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "captures must use their own database file")
	})

	t.Run("existing captures file is accepted", func(t *testing.T) {
		dir := t.TempDir()
		capturesPath := filepath.Join(dir, "captures.db")
		require.NoError(t, os.WriteFile(capturesPath, []byte("sqlite"), 0644))

		cfg, err := LoadConfigFromReader(strings.NewReader(
			capturesYAML(filepath.Join(dir, "activity.db"), capturesPath)))
		require.NoError(t, err)
		assert.Equal(t, capturesPath, cfg.Store.Captures.Path)
	})

	t.Run("maxSizeMB must be positive", func(t *testing.T) {
		dir := t.TempDir()
		for _, value := range []string{"0", "-1"} {
			_, err := LoadConfigFromReader(strings.NewReader(
				capturesYAML(filepath.Join(dir, "activity.db"), filepath.Join(dir, "captures.db"),
					"maxSizeMB: "+value)))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "store.captures.maxSizeMB must be >= 1")
		}
	})
}
