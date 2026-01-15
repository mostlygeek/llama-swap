package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPeerConfig_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "valid config",
			yaml: `
proxy: http://192.168.1.23
models:
  - model_a
  - model_b
`,
			wantErr: "",
		},
		{
			name: "valid config with apiKey",
			yaml: `
proxy: https://openrouter.ai/api
apiKey: sk-test-key
models:
  - meta-llama/llama-3.1-8b-instruct
`,
			wantErr: "",
		},
		{
			name: "missing proxy",
			yaml: `
models:
  - model_a
`,
			wantErr: "proxy is required",
		},
		{
			name: "empty proxy",
			yaml: `
proxy: ""
models:
  - model_a
`,
			wantErr: "proxy is required",
		},
		{
			name: "invalid proxy URL",
			yaml: `
proxy: "://invalid"
models:
  - model_a
`,
			wantErr: "invalid peer proxy URL",
		},
		{
			name: "missing models",
			yaml: `
proxy: http://localhost:8080
`,
			wantErr: "peer models can not be empty",
		},
		{
			name: "empty models",
			yaml: `
proxy: http://localhost:8080
models: []
`,
			wantErr: "peer models can not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config PeerConfig
			err := yaml.Unmarshal([]byte(tt.yaml), &config)

			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if !contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
			}
		})
	}
}

func TestPeerConfig_ProxyURL(t *testing.T) {
	yamlData := `
proxy: http://192.168.1.23:8080/api
apiKey: sk-test
models:
  - model_a
`
	var config PeerConfig
	err := yaml.Unmarshal([]byte(yamlData), &config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.ProxyURL == nil {
		t.Fatal("ProxyURL should not be nil")
	}

	if config.ProxyURL.Host != "192.168.1.23:8080" {
		t.Errorf("expected host %q, got %q", "192.168.1.23:8080", config.ProxyURL.Host)
	}

	if config.ProxyURL.Scheme != "http" {
		t.Errorf("expected scheme %q, got %q", "http", config.ProxyURL.Scheme)
	}

	if config.ProxyURL.Path != "/api" {
		t.Errorf("expected path %q, got %q", "/api", config.ProxyURL.Path)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestPeerFilters_SanitizedSetParams(t *testing.T) {
	tests := []struct {
		name         string
		setParams    map[string]any
		wantParams   map[string]any
		wantKeys     []string
	}{
		{
			name:       "empty setParams",
			setParams:  nil,
			wantParams: nil,
			wantKeys:   nil,
		},
		{
			name:       "empty map",
			setParams:  map[string]any{},
			wantParams: nil,
			wantKeys:   nil,
		},
		{
			name: "normal params",
			setParams: map[string]any{
				"temperature": 0.7,
				"top_p":       0.9,
			},
			wantParams: map[string]any{
				"temperature": 0.7,
				"top_p":       0.9,
			},
			wantKeys: []string{"temperature", "top_p"},
		},
		{
			name: "protected model param filtered",
			setParams: map[string]any{
				"model":       "should-be-filtered",
				"temperature": 0.7,
			},
			wantParams: map[string]any{
				"temperature": 0.7,
			},
			wantKeys: []string{"temperature"},
		},
		{
			name: "only protected param",
			setParams: map[string]any{
				"model": "should-be-filtered",
			},
			wantParams: nil,
			wantKeys:   nil,
		},
		{
			name: "complex nested values",
			setParams: map[string]any{
				"provider": map[string]any{
					"data_collection": "deny",
					"allow_fallbacks": false,
				},
				"transforms": []string{"middle-out"},
			},
			wantParams: map[string]any{
				"provider": map[string]any{
					"data_collection": "deny",
					"allow_fallbacks": false,
				},
				"transforms": []string{"middle-out"},
			},
			wantKeys: []string{"provider", "transforms"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := PeerFilters{SetParams: tt.setParams}
			gotParams, gotKeys := f.SanitizedSetParams()

			// Check keys
			if len(gotKeys) != len(tt.wantKeys) {
				t.Errorf("keys length mismatch: got %d, want %d", len(gotKeys), len(tt.wantKeys))
				return
			}
			for i, key := range gotKeys {
				if key != tt.wantKeys[i] {
					t.Errorf("key mismatch at %d: got %s, want %s", i, key, tt.wantKeys[i])
				}
			}

			// Check params
			if tt.wantParams == nil {
				if gotParams != nil {
					t.Errorf("expected nil params, got %v", gotParams)
				}
				return
			}

			if len(gotParams) != len(tt.wantParams) {
				t.Errorf("params length mismatch: got %d, want %d", len(gotParams), len(tt.wantParams))
			}

			for key, wantValue := range tt.wantParams {
				if gotValue, exists := gotParams[key]; !exists {
					t.Errorf("missing key: %s", key)
				} else {
					// Simple comparison for basic types
					switch v := wantValue.(type) {
					case string, int, float64, bool:
						if gotValue != v {
							t.Errorf("value mismatch for key %s: got %v, want %v", key, gotValue, v)
						}
					}
				}
			}
		})
	}
}

func TestPeerConfig_WithFilters(t *testing.T) {
	yamlData := `
proxy: https://openrouter.ai/api
apiKey: sk-test
models:
  - model_a
filters:
  setParams:
    temperature: 0.7
    provider:
      data_collection: deny
`
	var config PeerConfig
	err := yaml.Unmarshal([]byte(yamlData), &config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.Filters.SetParams == nil {
		t.Fatal("Filters.SetParams should not be nil")
	}

	if config.Filters.SetParams["temperature"] != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", config.Filters.SetParams["temperature"])
	}

	provider, ok := config.Filters.SetParams["provider"].(map[string]any)
	if !ok {
		t.Fatal("provider should be a map")
	}
	if provider["data_collection"] != "deny" {
		t.Errorf("expected data_collection deny, got %v", provider["data_collection"])
	}
}
