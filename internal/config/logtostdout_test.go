package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_ParseLogToStdout(t *testing.T) {
	tests := []struct {
		value                 string
		proxy, upstream, http bool
		wantErr               bool
	}{
		{value: "proxy", proxy: true},
		{value: "upstream", upstream: true},
		{value: "http", http: true},
		{value: "both", proxy: true, upstream: true, http: true},
		{value: "none"},
		{value: "proxy,http", proxy: true, http: true},
		{value: " upstream , http ", upstream: true, http: true},
		{value: "proxy,upstream,http", proxy: true, upstream: true, http: true},
		{value: "", wantErr: true},
		{value: "proxy,", wantErr: true},
		{value: "proxy,both", wantErr: true},
		{value: "stdout", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			proxy, upstream, http, err := ParseLogToStdout(tt.value)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.proxy, proxy, "proxy")
			assert.Equal(t, tt.upstream, upstream, "upstream")
			assert.Equal(t, tt.http, http, "http")
		})
	}
}

func TestConfig_LogToStdoutValidation(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader("logToStdout: proxy,http\n"))
	require.NoError(t, err)
	assert.Equal(t, "proxy,http", cfg.LogToStdout)

	cfg, err = LoadConfigFromReader(strings.NewReader("{}\n"))
	require.NoError(t, err)
	assert.Equal(t, LogToStdoutProxy, cfg.LogToStdout)

	_, err = LoadConfigFromReader(strings.NewReader("logToStdout: proxy,bogus\n"))
	assert.ErrorContains(t, err, "logToStdout")
}
