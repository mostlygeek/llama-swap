package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

// TestMetricsPassConfig_Defaults checks the global block's defaults are applied
// when the section is absent or partially set.
func TestMetricsPassConfig_Defaults(t *testing.T) {
	c, err := LoadConfigFromReader(strings.NewReader("startPort: 5800\n"))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	if c.MetricsPass.Every != 15*time.Second {
		t.Errorf("default every = %v, want 15s", c.MetricsPass.Every)
	}
	if c.MetricsPass.Visibility != MetricsPassVisibilityLoaded {
		t.Errorf("default visibility = %q, want %q", c.MetricsPass.Visibility, MetricsPassVisibilityLoaded)
	}
}

// TestMetricsPassConfig_Validate checks the global block rejects bad values.
func TestMetricsPassConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MetricsPassConfig
		wantErr bool
	}{
		{"valid loaded", MetricsPassConfig{Every: 15 * time.Second, Visibility: "loaded"}, false},
		{"valid always", MetricsPassConfig{Every: 30 * time.Second, Visibility: "always"}, false},
		{"too short", MetricsPassConfig{Every: time.Millisecond, Visibility: "loaded"}, true},
		{"bad visibility", MetricsPassConfig{Every: 15 * time.Second, Visibility: "sometimes"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestMetricsPassModelConfig_Enabled checks passthrough is enabled only when
// Path is set.
func TestMetricsPassModelConfig_Enabled(t *testing.T) {
	if (MetricsPassModelConfig{}).Enabled() {
		t.Error("Empty Path should not be enabled")
	}
	if !(MetricsPassModelConfig{Path: "/metrics"}).Enabled() {
		t.Error("Path set should be enabled")
	}
}

// TestMetricsPassModelConfig_ScrapeEvery checks the per-model cadence overrides
// the global one when set.
func TestMetricsPassModelConfig_ScrapeEvery(t *testing.T) {
	global := 15 * time.Second
	if got := (MetricsPassModelConfig{}).ScrapeEvery(global); got != global {
		t.Errorf("ScrapeEvery with no per-model value = %v, want global %v", got, global)
	}
	if got := (MetricsPassModelConfig{Every: 5 * time.Second}).ScrapeEvery(global); got != 5*time.Second {
		t.Errorf("ScrapeEvery with per-model value = %v, want 5s", got)
	}
}

// TestLoadConfig_MetricsPass checks the per-model block loads from YAML and the
// cadence validation runs.
func TestLoadConfig_MetricsPass(t *testing.T) {
	cfg := `
startPort: 5800
models:
  banana:
    cmd: echo hi
    proxy: http://localhost:1234
    metricsPass:
      path: /metrics
      every: 10s
`
	c, err := LoadConfigFromReader(strings.NewReader(cfg))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	mc := c.Models["banana"]
	if !mc.MetricsPass.Enabled() {
		t.Fatal("expected metricsPass to be enabled")
	}
	if mc.MetricsPass.Path != "/metrics" {
		t.Errorf("Path = %q, want /metrics", mc.MetricsPass.Path)
	}
	if got := mc.MetricsPass.ScrapeEvery(c.MetricsPass.Every); got != 10*time.Second {
		t.Errorf("ScrapeEvery = %v, want 10s", got)
	}
}

// TestLoadConfig_MetricsPassBadEvery checks a too-short per-model cadence is
// rejected.
func TestLoadConfig_MetricsPassBadEvery(t *testing.T) {
	cfg := `
startPort: 5800
models:
  banana:
    cmd: echo hi
    proxy: http://localhost:1234
    metricsPass:
      path: /metrics
      every: 1ms
`
	if _, err := LoadConfigFromReader(strings.NewReader(cfg)); err == nil {
		t.Fatal("expected an error for a too-short every")
	}
}

// TestLoadConfig_MetricsPassEveryZeroInheritsGlobal checks that a per-model
// every of 0 (or 0s) is accepted and falls back to the global cadence.
func TestLoadConfig_MetricsPassEveryZeroInheritsGlobal(t *testing.T) {
	for _, form := range []string{"0", "0s"} {
		t.Run(form, func(t *testing.T) {
			cfg := "metricsPass:\n  every: 15s\n" +
				"models:\n  banana:\n    cmd: echo hi\n    proxy: http://localhost:1234\n" +
				"    metricsPass:\n      path: /metrics\n      every: " + form + "\n"
			c, err := LoadConfigFromReader(strings.NewReader(cfg))
			if err != nil {
				t.Fatalf("every: %q should be accepted: %v", form, err)
			}
			mc := c.Models["banana"]
			if mc.MetricsPass.Every != 0 {
				t.Errorf("Every = %v, want 0", mc.MetricsPass.Every)
			}
			if got := mc.MetricsPass.ScrapeEvery(c.MetricsPass.Every); got != 15*time.Second {
				t.Errorf("ScrapeEvery = %v, want 15s (global)", got)
			}
		})
	}
}

// TestConfig_MetricsPassSchema checks that the per-model metricsPass schema
// agrees with the loader on the documented values: path "" (disable
// passthrough) and every 0 (inherit the global cadence) are valid, while a
// non-empty path without a leading slash is rejected.
func TestConfig_MetricsPassSchema(t *testing.T) {
	data, err := os.ReadFile("../../config-schema.json")
	if err != nil {
		t.Fatalf("read config-schema.json: %v", err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{BaseURI: "https://github.com/mostlygeek/llama-swap/"})
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}

	// modelWith builds a minimal config with one model whose block is the
	// given per-model lines (indented for the models entry).
	modelWith := func(block string) string {
		return "startPort: 5800\nmodels:\n  banana:\n    cmd: echo hi\n    proxy: http://localhost:1234\n" + block
	}

	for _, tc := range []struct {
		name  string
		input string
		valid bool
	}{
		{"disable and inherit", modelWith("    metricsPass:\n      path: ''\n      every: 0\n"), true},
		{"path with cadence", modelWith("    metricsPass:\n      path: /metrics\n      every: 15s\n"), true},
		{"path inherits cadence", modelWith("    metricsPass:\n      path: /metrics\n"), true},
		{"path without slash", modelWith("    metricsPass:\n      path: metrics\n"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var instance any
			if err := yaml.Unmarshal([]byte(tc.input), &instance); err != nil {
				t.Fatalf("yaml unmarshal: %v", err)
			}
			b, err := json.Marshal(instance)
			if err != nil {
				t.Fatalf("json marshal: %v", err)
			}
			if err := json.Unmarshal(b, &instance); err != nil {
				t.Fatalf("json unmarshal: %v", err)
			}
			err = resolved.Validate(instance)
			if tc.valid {
				if err != nil {
					t.Fatalf("schema rejected a value the loader accepts:\n%v", err)
				}
				if _, err := LoadConfigFromReader(strings.NewReader(tc.input)); err != nil {
					t.Fatalf("schema and loader must both accept the example: %v", err)
				}
			} else if err == nil {
				t.Fatal("schema accepted an invalid metricsPass value")
			}
		})
	}
}
