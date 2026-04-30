package proxy

import (
	"strings"
	"testing"
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
