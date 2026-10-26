package config

import (
	"strings"
	"testing"
	"time"
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
