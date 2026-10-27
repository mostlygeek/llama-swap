package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// metricsPassDefaultEvery is how often a model's upstream metrics are scraped
// when neither the global nor the per-model cadence is set.
const metricsPassDefaultEvery = 15 * time.Second

// Valid visibility values for metricsPass.visibility.
const (
	MetricsPassVisibilityLoaded = "loaded" // section shown only while the model is running
	MetricsPassVisibilityAlways = "always" // last scrape kept even after the model unloads
)

// MetricsPassConfig holds the global settings for passing a model's upstream
// Prometheus metrics through llama-swap's /metrics endpoint.
type MetricsPassConfig struct {
	// Every is the default scrape cadence for a model's upstream metrics.
	// A per-model metricsPass.every overrides it when set.
	Every time.Duration `yaml:"every"`

	// Visibility controls when a model's passed metrics are shown on
	// /metrics. "loaded" (default) hides them once the model unloads;
	// "always" keeps the last successful scrape.
	Visibility string `yaml:"visibility"`
}

// UnmarshalYAML applies the metricsPass defaults.
func (c *MetricsPassConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawMetricsPassConfig MetricsPassConfig
	defaults := rawMetricsPassConfig{
		Every:      metricsPassDefaultEvery,
		Visibility: MetricsPassVisibilityLoaded,
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	*c = MetricsPassConfig(defaults)
	return nil
}

// Validate checks the metricsPass values and returns an error if invalid.
func (c *MetricsPassConfig) Validate() error {
	if c.Every < time.Second {
		return fmt.Errorf("every must be at least 1s, got %v", c.Every)
	}
	switch c.Visibility {
	case MetricsPassVisibilityLoaded, MetricsPassVisibilityAlways:
		return nil
	default:
		return fmt.Errorf("visibility must be %q or %q, got %q", MetricsPassVisibilityLoaded, MetricsPassVisibilityAlways, c.Visibility)
	}
}

// MetricsPassModelConfig holds a model's settings for passing its upstream
// Prometheus metrics through llama-swap's /metrics endpoint. The scrape is
// namespaced with the model name as a metric-name prefix.
type MetricsPassModelConfig struct {
	// Path is the upstream endpoint to scrape for the model's metrics,
	// such as "/metrics". Empty disables passthrough for this model.
	Path string `yaml:"path"`

	// Every overrides the global metricsPass.every for this model.
	// 0 (or "0s") uses the global cadence.
	Every time.Duration `yaml:"every"`
}

// rawMetricsPassModelConfig is the plain form used to decode the metricsPass
// block. Every is decoded as any so the unmarshaler can accept both a duration
// string ("15s") and the bare zero ("0", 0), which the loader documents as
// "use the global cadence".
type rawMetricsPassModelConfig struct {
	Path  string `yaml:"path"`
	Every any    `yaml:"every"`
}

// UnmarshalYAML decodes the metricsPass block. Every accepts a duration string
// ("15s") or the bare zero (0); anything else is rejected.
func (c *MetricsPassModelConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw rawMetricsPassModelConfig
	if err := node.Decode(&raw); err != nil {
		return err
	}
	c.Path = raw.Path
	switch v := raw.Every.(type) {
	case nil:
		// no value set: inherit the global cadence
	case string:
		if v != "" && v != "0" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("metricsPass.every: invalid duration %q", v)
			}
			c.Every = d
		}
	case int:
		if v != 0 {
			return fmt.Errorf("metricsPass.every: expected a duration or 0, got %d", v)
		}
	case float64:
		if v != 0 {
			return fmt.Errorf("metricsPass.every: expected a duration or 0, got %v", v)
		}
	default:
		return fmt.Errorf("metricsPass.every: expected a duration or 0, got %T", raw.Every)
	}
	return nil
}

// Enabled reports whether the model's metrics passthrough is configured.
// It is enabled only when Path is set; the cadence falls back to the global
// value when Every is 0.
func (c MetricsPassModelConfig) Enabled() bool {
	return c.Path != ""
}

// ScrapeEvery returns the cadence to use for this model's scrape. It is the
// per-model every when set, otherwise the global default.
func (c MetricsPassModelConfig) ScrapeEvery(global time.Duration) time.Duration {
	if c.Every > 0 {
		return c.Every
	}
	return global
}

// validateMetricsPass checks a model's metricsPass block. Only the cadence is
// validated; Path may point at any endpoint, so it is not checked here.
func validateMetricsPass(modelId string, c MetricsPassModelConfig) error {
	if c.Every < 0 {
		return fmt.Errorf("model %s: metricsPass.every must be >= 0, got %v", modelId, c.Every)
	}
	if c.Every > 0 && c.Every < time.Second {
		return fmt.Errorf("model %s: metricsPass.every must be at least 1s, got %v", modelId, c.Every)
	}
	return nil
}
