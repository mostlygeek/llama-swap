package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestConfig_UIActivitySessionID(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "defaults",
			want: []string{"X-Session-ID", "X-Litellm-Session-Id"},
		},
		{
			name: "normalizes configured headers",
			yaml: "ui:\n  activity:\n    session_id: [' X-Trace-ID ', '', x-trace-id, X-Session-ID]\n",
			want: []string{"X-Trace-ID", "X-Session-ID"},
		},
		{
			name: "explicit empty list disables lookup",
			yaml: "ui:\n  activity:\n    session_id: []\n",
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadConfigFromReader(strings.NewReader(tc.yaml))
			if err != nil {
				t.Fatalf("LoadConfigFromReader: %v", err)
			}
			if !reflect.DeepEqual(cfg.UI.Activity.SessionID, tc.want) {
				t.Errorf("session id headers = %#v, want %#v", cfg.UI.Activity.SessionID, tc.want)
			}
		})
	}
}
