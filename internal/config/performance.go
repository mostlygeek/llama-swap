package config

import (
	"fmt"
	"time"
)

// PerformanceConfig holds configuration for system performance monitoring
type PerformanceConfig struct {
	Disabled bool          `yaml:"disabled"`
	Every    time.Duration `yaml:"every"`

	// GpuBudgetMB is an optional soft ceiling, in MiB, on total GPU-managed
	// memory (VRAM + GTT, as reported by the perf monitor). When > 0 and the
	// monitor is active, the router evicts least-recently-used models before
	// a swap so the projected footprint stays under budget. 0 (default)
	// disables the gate entirely. See internal/router/memgate.go.
	GpuBudgetMB int `yaml:"gpuBudgetMB"`
}

func (p *PerformanceConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawPerformanceConfig PerformanceConfig
	defaults := rawPerformanceConfig{
		Every: 5 * time.Second,
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	*p = PerformanceConfig(defaults)
	return nil
}

// Validate checks the PerformanceConfig values and returns an error if invalid
func (p *PerformanceConfig) Validate() error {
	if p.Every < 5*time.Second {
		return fmt.Errorf("every must be at least 5s, got %v", p.Every)
	}
	return nil
}
