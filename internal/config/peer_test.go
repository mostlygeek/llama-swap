package config

import (
	"strings"
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

func TestPeerConfig_WithBothFilters(t *testing.T) {
	yamlData := `
proxy: https://openrouter.ai/api
apiKey: sk-test
models:
  - model_a
filters:
  stripParams: "temperature, top_p"
  setParams:
    max_tokens: 1000
`
	var config PeerConfig
	err := yaml.Unmarshal([]byte(yamlData), &config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check stripParams
	stripParams := config.Filters.SanitizedStripParams()
	if len(stripParams) != 2 {
		t.Errorf("expected 2 strip params, got %d", len(stripParams))
	}
	if stripParams[0] != "temperature" || stripParams[1] != "top_p" {
		t.Errorf("unexpected strip params: %v", stripParams)
	}

	// Check setParams
	if config.Filters.SetParams == nil {
		t.Fatal("Filters.SetParams should not be nil")
	}
	if config.Filters.SetParams["max_tokens"] != 1000 {
		t.Errorf("expected max_tokens 1000, got %v", config.Filters.SetParams["max_tokens"])
	}
}

func TestConfig_ResolvePeerModel(t *testing.T) {
	cfg := Config{
		Models: map[string]ModelConfig{"shared": {}},
		Peers: PeerDictionaryConfig{
			"p1": {Models: []string{"shared", "org/model"}},
			"p2": {Models: []string{"shared", "unique"}},
		},
	}

	tests := []struct {
		search      string
		wantPeerID  string
		wantModelID string
		wantFound   bool
	}{
		{search: "p1/shared", wantPeerID: "p1", wantModelID: "shared", wantFound: true},
		{search: "p2/shared", wantPeerID: "p2", wantModelID: "shared", wantFound: true},
		{search: "p1/org/model", wantPeerID: "p1", wantModelID: "org/model", wantFound: true},
		{search: "unique", wantPeerID: "p2", wantModelID: "unique", wantFound: true},
		{search: "shared", wantFound: false},
		{search: "missing", wantFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.search, func(t *testing.T) {
			peerID, modelID, found := cfg.ResolvePeerModel(tt.search)
			if peerID != tt.wantPeerID || modelID != tt.wantModelID || found != tt.wantFound {
				t.Fatalf(
					"ResolvePeerModel(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.search, peerID, modelID, found,
					tt.wantPeerID, tt.wantModelID, tt.wantFound,
				)
			}
		})
	}

	if got, found := cfg.ResolveBaseModel("shared"); !found || got != "shared" {
		t.Fatalf("ResolveBaseModel(shared) = (%q, %v), want local model", got, found)
	}
	if got, found := cfg.ResolveBaseModel("p1/shared"); !found || got != "p1/shared" {
		t.Fatalf("ResolveBaseModel(p1/shared) = (%q, %v), want peer FQN", got, found)
	}
}

func TestConfig_ResolvePeerModel_FQNPrecedesBareName(t *testing.T) {
	cfg := Config{Peers: PeerDictionaryConfig{
		"p1": {Models: []string{"model"}},
		"p2": {Models: []string{"p1/model"}},
	}}

	peerID, modelID, found := cfg.ResolvePeerModel("p1/model")
	if !found || peerID != "p1" || modelID != "model" {
		t.Fatalf("ResolvePeerModel(p1/model) = (%q, %q, %v), want p1/model FQN", peerID, modelID, found)
	}

	peerID, modelID, found = cfg.ResolvePeerModel("p2/p1/model")
	if !found || peerID != "p2" || modelID != "p1/model" {
		t.Fatalf("ResolvePeerModel(p2/p1/model) = (%q, %q, %v), want p2 raw slash model", peerID, modelID, found)
	}
}

func TestConfig_ValidatePeerNamespace(t *testing.T) {
	t.Run("ambiguous constructed name", func(t *testing.T) {
		cfg := Config{Peers: PeerDictionaryConfig{
			"a":   {Models: []string{"b/c"}},
			"a/b": {Models: []string{"c"}},
		}}
		if err := ValidatePeerNamespace(cfg); err == nil {
			t.Fatal("expected ambiguous peer FQN error")
		}
	})

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "local model",
			mutate: func(cfg *Config) {
				cfg.Models["peer/model"] = ModelConfig{}
			},
		},
		{
			name: "local alias",
			mutate: func(cfg *Config) {
				cfg.Models["local"] = ModelConfig{Aliases: []string{"peer/model"}}
			},
		},
		{
			name: "selector",
			mutate: func(cfg *Config) {
				cfg.Selectors["peer/model"] = SelectorConfig{}
			},
		},
		{
			name: "profile pin",
			mutate: func(cfg *Config) {
				cfg.Profiles["profile"] = ProfileConfig{Pins: map[string]string{"peer/model": "local"}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Models:    map[string]ModelConfig{},
				Peers:     PeerDictionaryConfig{"peer": {Models: []string{"model"}}},
				Selectors: map[string]SelectorConfig{},
				Profiles:  map[string]ProfileConfig{},
			}
			tt.mutate(&cfg)
			if err := ValidatePeerNamespace(cfg); err == nil {
				t.Fatal("expected reserved peer FQN conflict")
			}
		})
	}
}

func TestConfig_LoadPeerNamespaceConflict(t *testing.T) {
	_, err := LoadConfigFromReader(strings.NewReader(`
models:
  local:
    cmd: echo ${PORT}
    filters:
      setParamsByID:
        peer/model:
          temperature: 0.5
peers:
  peer:
    proxy: http://example.com
    models: [model]
`))
	if err == nil || !strings.Contains(err.Error(), "conflicts with fully qualified peer model name") {
		t.Fatalf("LoadConfigFromReader error = %v, want peer FQN conflict", err)
	}
}
