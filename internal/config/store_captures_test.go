package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_StoreCaptures(t *testing.T) {
	t.Run("absent keeps the legacy in-memory behavior", func(t *testing.T) {
		cfg, err := LoadConfigFromReader(strings.NewReader(`
store:
  path: ./llama-swap.db
`))
		require.NoError(t, err)
		require.NotNil(t, cfg.Store)
		assert.Nil(t, cfg.Store.Captures, "no captures section must leave captures in memory")
		assert.Equal(t, 5, cfg.CaptureBuffer, "the legacy default buffer must survive")
		assert.Equal(t, 1000, cfg.MetricsMaxInMemory)
	})

	t.Run("no store section at all", func(t *testing.T) {
		cfg, err := LoadConfigFromReader(strings.NewReader(`logLevel: info`))
		require.NoError(t, err)
		assert.Nil(t, cfg.Store)
		assert.Equal(t, 5, cfg.CaptureBuffer)
	})

	t.Run("defaults", func(t *testing.T) {
		dir := t.TempDir()
		cfg, err := LoadConfigFromReader(strings.NewReader(`
store:
  path: ` + filepath.Join(dir, "activity.db") + `
  captures:
    path: ` + filepath.Join(dir, "captures.db") + `
`))
		require.NoError(t, err)
		require.NotNil(t, cfg.Store.Captures)
		assert.Equal(t, StoreCapturesDefaultMaxSizeMB, cfg.Store.Captures.MaxSizeMB)
		assert.Equal(t, StoreCapturesDefaultMaxCaptureMB, cfg.Store.Captures.MaxCaptureMB)
	})

	t.Run("explicit values", func(t *testing.T) {
		dir := t.TempDir()
		cfg, err := LoadConfigFromReader(strings.NewReader(`
store:
  path: ` + filepath.Join(dir, "activity.db") + `
  captures:
    path: ` + filepath.Join(dir, "captures.db") + `
    maxSizeMB: 2048
    maxCaptureMB: 25
`))
		require.NoError(t, err)
		require.NotNil(t, cfg.Store.Captures)
		assert.Equal(t, 2048, cfg.Store.Captures.MaxSizeMB)
		assert.Equal(t, 25, cfg.Store.Captures.MaxCaptureMB)
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

	t.Run("legacy captureBuffer is rejected alongside", func(t *testing.T) {
		dir := t.TempDir()
		_, err := LoadConfigFromReader(strings.NewReader(`
captureBuffer: 20
store:
  path: ` + filepath.Join(dir, "activity.db") + `
  captures:
    path: ` + filepath.Join(dir, "captures.db") + `
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "store.captures supersedes the legacy captureBuffer setting")
	})

	t.Run("legacy captureBuffer zero is still a conflict", func(t *testing.T) {
		// captureBuffer: 0 means "captures off", which is a decision the new
		// section owns; accepting it alongside store.captures would make the
		// effective behavior depend on which default won.
		dir := t.TempDir()
		_, err := LoadConfigFromReader(strings.NewReader(`
captureBuffer: 0
store:
  path: ` + filepath.Join(dir, "activity.db") + `
  captures:
    path: ` + filepath.Join(dir, "captures.db") + `
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "captureBuffer")
	})

	t.Run("legacy metricsMaxInMemory is rejected alongside", func(t *testing.T) {
		dir := t.TempDir()
		_, err := LoadConfigFromReader(strings.NewReader(`
metricsMaxInMemory: 500
store:
  path: ` + filepath.Join(dir, "activity.db") + `
  captures:
    path: ` + filepath.Join(dir, "captures.db") + `
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "store.captures supersedes the legacy metricsMaxInMemory setting")
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
		_, err := LoadConfigFromReader(strings.NewReader(`
store:
  path: ` + filepath.Join(dir, "activity.db") + `
  captures:
    path: ""
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "store.captures.path must not be empty")
	})

	t.Run("unwritable captures directory rejected", func(t *testing.T) {
		dir := t.TempDir()
		_, err := LoadConfigFromReader(strings.NewReader(`
store:
  path: ` + filepath.Join(dir, "activity.db") + `
  captures:
    path: /no/such/dir/captures.db
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "store.captures.path")
		assert.Contains(t, err.Error(), "not writable")
	})

	t.Run("sharing the activity database is rejected", func(t *testing.T) {
		dir := t.TempDir()
		shared := filepath.Join(dir, "store.db")
		_, err := LoadConfigFromReader(strings.NewReader(`
store:
  path: ` + shared + `
  captures:
    path: ` + shared + `
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "captures must use their own database file")
	})

	t.Run("relative and absolute spellings of one file are rejected", func(t *testing.T) {
		dir := t.TempDir()
		abs, err := filepath.Abs(filepath.Join(dir, "store.db"))
		require.NoError(t, err)
		_, err = LoadConfigFromReader(strings.NewReader(`
store:
  path: ` + abs + `
  captures:
    path: ` + filepath.Join(dir, ".", "store.db") + `
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "captures must use their own database file")
	})

	t.Run("existing captures file is accepted", func(t *testing.T) {
		dir := t.TempDir()
		capturesPath := filepath.Join(dir, "captures.db")
		require.NoError(t, os.WriteFile(capturesPath, []byte("sqlite"), 0644))

		cfg, err := LoadConfigFromReader(strings.NewReader(`
store:
  path: ` + filepath.Join(dir, "activity.db") + `
  captures:
    path: ` + capturesPath + `
`))
		require.NoError(t, err)
		assert.Equal(t, capturesPath, cfg.Store.Captures.Path)
	})

	t.Run("size limits must be positive", func(t *testing.T) {
		dir := t.TempDir()
		base := `
store:
  path: ` + filepath.Join(dir, "activity.db") + `
  captures:
    path: ` + filepath.Join(dir, "captures.db") + `
`
		for _, tc := range []struct {
			name     string
			yaml     string
			contains string
		}{
			{"maxSizeMB zero", base + "    maxSizeMB: 0\n", "store.captures.maxSizeMB must be >= 1"},
			{"maxSizeMB negative", base + "    maxSizeMB: -1\n", "store.captures.maxSizeMB must be >= 1"},
			{"maxCaptureMB zero", base + "    maxCaptureMB: 0\n", "store.captures.maxCaptureMB must be >= 1"},
			{"maxCaptureMB above budget", base + "    maxSizeMB: 5\n    maxCaptureMB: 10\n", "larger than store.captures.maxSizeMB"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := LoadConfigFromReader(strings.NewReader(tc.yaml))
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.contains)
			})
		}
	})
}
