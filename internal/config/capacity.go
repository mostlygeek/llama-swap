package config

import (
	"fmt"
	"math"

	"gopkg.in/yaml.v3"
)

// CapacityConfig declares an operator-measured budget in a common integer unit
// (for example MiB). Usage and eviction preference are declared on each model.
type CapacityConfig struct {
	Total int64 `yaml:"total"`
}

type ModelCapacityConfig struct {
	Usage            int64 `yaml:"usage"`
	EvictionPriority int64 `yaml:"evictionPriority"`
}

func (c *CapacityConfig) UnmarshalYAML(node *yaml.Node) error {
	type rawCapacity CapacityConfig
	var raw rawCapacity
	if err := decodeCapacity(node, &raw, "total"); err != nil {
		return err
	}
	*c = CapacityConfig(raw)
	return nil
}

func (c *ModelCapacityConfig) UnmarshalYAML(node *yaml.Node) error {
	type rawCapacity ModelCapacityConfig
	var raw rawCapacity
	if err := decodeCapacity(node, &raw, "usage", "evictionPriority"); err != nil {
		return err
	}
	*c = ModelCapacityConfig(raw)
	return nil
}

// YAML's numeric conversion can silently truncate floats. Both capacity blocks
// accept only integer fields, rejecting fractions and misspelled settings.
func decodeCapacity(node *yaml.Node, dst any, fields ...string) error {
	if err := node.Decode(dst); err != nil {
		return err
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		known := false
		for _, field := range fields {
			if key == field {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("unknown capacity setting %q", key)
		}
		if value.Tag != "!!int" {
			return fmt.Errorf("capacity.%s must be an integer", key)
		}
	}
	return nil
}

func (c ModelCapacityConfig) Validate() error {
	if c.Usage <= 0 {
		return fmt.Errorf("capacity.usage must be a positive integer")
	}
	if c.EvictionPriority < 0 {
		return fmt.Errorf("capacity.evictionPriority must be non-negative")
	}
	return nil
}

// ValidateCapacity rejects incomplete budgets and bounds the sum used by the
// planner so usage arithmetic cannot wrap around. Priorities are compared,
// rather than added, so their magnitude cannot overflow a solution score.
func ValidateCapacity(c *CapacityConfig, models map[string]ModelConfig) error {
	if c == nil {
		return fmt.Errorf("capacity configuration is required")
	}
	if c.Total <= 0 {
		return fmt.Errorf("total must be a positive integer")
	}
	var usage int64
	for id, model := range models {
		if model.Capacity == nil {
			return fmt.Errorf("model %q: capacity.usage is required", id)
		}
		if err := model.Capacity.Validate(); err != nil {
			return fmt.Errorf("model %q: %w", id, err)
		}
		amount := model.Capacity.Usage
		if amount > c.Total {
			return fmt.Errorf("model %q: capacity.usage (%d) exceeds total (%d)", id, amount, c.Total)
		}
		if amount > math.MaxInt64-usage {
			return fmt.Errorf("sum of capacity.usage exceeds int64 range")
		}
		usage += amount
	}
	return nil
}
