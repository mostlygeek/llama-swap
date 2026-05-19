package config

import (
	"fmt"
	"time"
)

// PerformanceConfig holds configuration for system performance monitoring
type PerformanceConfig struct {
	Disabled bool          `yaml:"disabled"`
	Every    time.Duration `yaml:"every"`
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
