package config

import (
	"fmt"
	"time"
)

// PerformanceConfig holds configuration for system performance monitoring
type PerformanceConfig struct {
	Enable bool          `yaml:"enable"`
	Every  time.Duration `yaml:"every"`
	MaxAge time.Duration `yaml:"maxAge"`
	GC     time.Duration `yaml:"gc"`
}

func (p *PerformanceConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawPerformanceConfig PerformanceConfig
	defaults := rawPerformanceConfig{
		Enable: true,
		Every:  15 * time.Second,
		MaxAge: 1 * time.Hour,
		GC:     5 * time.Minute,
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	*p = PerformanceConfig(defaults)
	return nil
}

// Validate checks the PerformanceConfig values and returns an error if invalid
func (p *PerformanceConfig) Validate() error {
	if p.Every < time.Second {
		return fmt.Errorf("every must be at least 1s, got %v", p.Every)
	}
	if p.MaxAge <= 0 {
		return fmt.Errorf("maxAge must be greater than 0, got %v", p.MaxAge)
	}
	if p.GC <= 0 {
		return fmt.Errorf("gc must be greater than 0, got %v", p.GC)
	}
	return nil
}
