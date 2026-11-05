package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mostlygeek/llama-swap/internal/config"
)

func TestListenURL(t *testing.T) {
	tests := []struct {
		name   string
		scheme string
		addr   string
		want   string
	}{
		{name: "default HTTP wildcard", scheme: "http", addr: ":8080", want: "http://0.0.0.0:8080"},
		{name: "default HTTPS wildcard", scheme: "https", addr: ":8443", want: "https://0.0.0.0:8443"},
		{name: "explicit IPv4", scheme: "http", addr: "127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{name: "explicit IPv6", scheme: "https", addr: "[::1]:8443", want: "https://[::1]:8443"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, listenURL(tt.scheme, tt.addr))
		})
	}
}

func TestConfigureTailcatListener_RequiresModels(t *testing.T) {
	cfg := config.Config{}
	err := configureTailcatListener(&cfg, "/path/to/server.private.json")
	require.ErrorContains(t, err, "tailcat.models")

	cfg.Tailcat = &config.TailcatConfig{}
	err = configureTailcatListener(&cfg, "/path/to/server.private.json")
	require.ErrorContains(t, err, "tailcat.models")

	cfg.Tailcat.Models = []string{"model"}
	require.NoError(t, configureTailcatListener(&cfg, "/path/to/server.private.json"))
	assert.True(t, cfg.TailcatEnabled())
}

func TestConfigureTailcatListener_DisabledWithoutFlag(t *testing.T) {
	cfg := config.Config{}
	require.NoError(t, configureTailcatListener(&cfg, ""))
	assert.False(t, cfg.TailcatEnabled())
}

func TestLoadTailcatListenerKey_InvalidFile(t *testing.T) {
	_, err := loadTailcatListenerKey(filepath.Join(t.TempDir(), "missing.private.json"), true)
	require.ErrorContains(t, err, "-listen-tailcat")
}

func TestRunValidate_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
models:
  model1:
    cmd: server --port ${PORT}
    proxy: http://localhost:8080
`), 0644))

	var buf strings.Builder
	code := runValidate(configPath, "", &buf)

	assert.Equal(t, 0, code)
	assert.NotEmpty(t, buf.String())
	assert.Contains(t, buf.String(), "config is valid")
}

func TestRunValidate_BrokenConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
models:
  model1:
    cmd: server --port ${PORT} ${UNKNOWN_MACRO}
    proxy: http://localhost:8080
`), 0644))

	var buf strings.Builder
	code := runValidate(configPath, "", &buf)

	assert.Equal(t, 1, code)
	assert.NotEmpty(t, buf.String())
	assert.Contains(t, buf.String(), "failed")
}

func TestRunValidate_ConfigDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.yml"), []byte(`
models:
  model1:
    cmd: server --port ${PORT}
    proxy: http://localhost:8080
`), 0644))

	var buf strings.Builder
	code := runValidate("", dir, &buf)

	assert.Equal(t, 0, code)
	assert.NotEmpty(t, buf.String())
	assert.Contains(t, buf.String(), "config is valid")
}
