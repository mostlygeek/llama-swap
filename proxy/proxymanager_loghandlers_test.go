package proxy

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogMonitorIdQueryParameterStripping(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "upstream without query param",
			input:    "upstream",
			expected: "upstream",
		},
		{
			name:     "upstream with query param",
			input:    "upstream?no-history",
			expected: "upstream",
		},
		{
			name:     "proxy with multiple query params",
			input:    "proxy?no-history&foo=bar",
			expected: "proxy",
		},
		{
			name:     "model with slash and query param",
			input:    "author/model?no-history",
			expected: "author/model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the query parameter stripping logic
			logMonitorId := tt.input
			if idx := strings.Index(logMonitorId, "?"); idx != -1 {
				logMonitorId = logMonitorId[:idx]
			}

			if logMonitorId != tt.expected {
				t.Errorf("Query parameter stripping failed: got %q, want %q", logMonitorId, tt.expected)
			}
		})
	}
}

// TestProxyManager_GetLogger_ProcessGroups verifies getLogger resolves the
// well-known "proxy"/"upstream" loggers and a model ID managed by processGroups.
func TestProxyManager_GetLogger_ProcessGroups(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model1:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model1
`)
	pm := New(cfg)
	defer pm.StopProcesses(StopImmediately)

	tests := []struct {
		id      string
		wantErr bool
	}{
		{"proxy", false},
		{"upstream", false},
		{"model1", false},
		{"does-not-exist", true},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			logger, err := pm.getLogger(tt.id)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid logger")
			} else {
				require.NoError(t, err)
				assert.NotNil(t, logger)
			}
		})
	}
}

// TestProxyManager_GetLogger_Matrix verifies that getLogger can resolve a model
// ID when the proxy is configured with a swap matrix (pm.processGroups is empty
// for matrix-managed models).
func TestProxyManager_GetLogger_Matrix(t *testing.T) {
	cfg := config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		ExpandedSets: []config.ExpandedSet{
			{SetName: "s1", Models: []string{"model1", "model2"}},
		},
		Matrix: &config.MatrixConfig{},
	}

	pm := New(cfg)
	defer pm.StopProcesses(StopImmediately)

	tests := []struct {
		id      string
		wantErr bool
	}{
		{"proxy", false},
		{"upstream", false},
		{"model1", false},
		{"model2", false},
		{"does-not-exist", true},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			logger, err := pm.getLogger(tt.id)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid logger")
			} else {
				require.NoError(t, err)
				assert.NotNil(t, logger)
			}
		})
	}
}

// TestProxyManager_StreamLogs_Matrix verifies that /logs/stream/<modelID>
// returns 200 (not 400) for a model managed by the swap matrix.
func TestProxyManager_StreamLogs_Matrix(t *testing.T) {
	cfg := config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"matrix-model": getTestSimpleResponderConfig("matrix-model"),
		},
		ExpandedSets: []config.ExpandedSet{
			{SetName: "s1", Models: []string{"matrix-model"}},
		},
		Matrix: &config.MatrixConfig{},
	}

	pm := New(cfg)
	defer pm.StopProcesses(StopImmediately)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest("GET", "/logs/stream/matrix-model", nil)
	req = req.WithContext(ctx)
	rec := CreateTestResponseRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pm.ServeHTTP(rec, req)
	}()

	<-ctx.Done()
	<-done

	assert.Equal(t, 200, rec.Code)
}
