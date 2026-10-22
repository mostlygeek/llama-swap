package config

import (
	"fmt"
	"math"
	"strings"

	"gopkg.in/yaml.v3"
)

// CapacityConfig declares independent resource limits in operator-chosen units.
// Each model uses the same units as the pools it references.
type CapacityConfig struct {
	Pools map[string]int64 `yaml:"pools"`
}

// UnmarshalYAML accepts only a pools mapping with integer resource limits.
func (c *CapacityConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("capacity must be a mapping")
	}
	for i := 0; i < len(node.Content); i += 2 {
		if key := node.Content[i].Value; key != "pools" {
			return fmt.Errorf("unknown capacity setting %q", key)
		}
		if err := validateCapacityMap(node.Content[i+1], "capacity.pools"); err != nil {
			return err
		}
	}
	type rawCapacity CapacityConfig
	return node.Decode((*rawCapacity)(c))
}

// validateCapacityMap checks pool names and integer YAML tags before decoding
// so yaml.v3 cannot silently truncate fractional resource values.
func validateCapacityMap(node *yaml.Node, path string) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must be a mapping", path)
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Tag != "!!str" || strings.TrimSpace(key.Value) == "" {
			return fmt.Errorf("%s pool names must be nonempty strings", path)
		}
		if value.Tag != "!!int" {
			return fmt.Errorf("%s.%s must be an integer", path, key.Value)
		}
	}
	return nil
}

// validateModelCapacity rejects invalid usage and costs independently of the
// selected router; pool references are checked when capacity routing is active.
func validateModelCapacity(model ModelConfig) error {
	if model.EvictCost < 0 {
		return fmt.Errorf("evict_cost must be a nonnegative integer")
	}
	for pool, usage := range model.Capacities {
		if strings.TrimSpace(pool) == "" {
			return fmt.Errorf("capacities pool names must be nonempty strings")
		}
		if usage < 0 {
			return fmt.Errorf("capacities.%s must be a nonnegative integer", pool)
		}
	}
	return nil
}

// ValidateCapacity rejects impossible requirements and bounds all arithmetic
// used by the planner. Omitted capacities consume zero resources.
func ValidateCapacity(c *CapacityConfig, models map[string]ModelConfig) error {
	if c == nil {
		return fmt.Errorf("capacity configuration is required")
	}
	if len(c.Pools) == 0 {
		return fmt.Errorf("pools must contain at least one resource limit")
	}
	for pool, total := range c.Pools {
		if strings.TrimSpace(pool) == "" {
			return fmt.Errorf("pool names must be nonempty strings")
		}
		if total <= 0 {
			return fmt.Errorf("pool %q total must be a positive integer", pool)
		}
	}
	sums := make(map[string]int64, len(c.Pools))
	var cost int64
	for id, model := range models {
		if err := validateModelCapacity(model); err != nil {
			return fmt.Errorf("model %q: %w", id, err)
		}
		for pool, usage := range model.Capacities {
			total, ok := c.Pools[pool]
			if !ok {
				return fmt.Errorf("model %q: unknown capacity pool %q", id, pool)
			}
			if usage > total {
				return fmt.Errorf("model %q: usage (%d) exceeds total (%d) for pool %q", id, usage, total, pool)
			}
			if usage > math.MaxInt64-sums[pool] {
				return fmt.Errorf("sum of capacities exceeds int64 range for pool %q", pool)
			}
			sums[pool] += usage
		}
		if model.EvictCost > math.MaxInt64-cost {
			return fmt.Errorf("sum of evict_cost exceeds int64 range")
		}
		cost += model.EvictCost
	}
	return nil
}
