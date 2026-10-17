package config

import (
	"fmt"
	"math"

	"gopkg.in/yaml.v3"
)

// ResourceConfig declares one operator-measured resource pool. Capacity and
// requirements use the same integer unit (for example MiB); nothing is measured
// automatically. Keys are real local model IDs, as in matrix evict_costs.
type ResourceConfig struct {
	Capacity     int64            `yaml:"capacity"`
	Requirements map[string]int64 `yaml:"requirements"`
	EvictCosts   map[string]int   `yaml:"evict_costs"`
}

// UnmarshalYAML rejects fractional declarations instead of letting YAML's
// numeric conversion silently truncate a resource amount or eviction cost.
func (r *ResourceConfig) UnmarshalYAML(node *yaml.Node) error {
	type rawResource ResourceConfig
	var raw rawResource
	if err := node.Decode(&raw); err != nil {
		return err
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		switch key {
		case "capacity":
			if value.Tag != "!!int" {
				return fmt.Errorf("resource capacity must be an integer")
			}
		case "requirements", "evict_costs":
			for j := 1; j < len(value.Content); j += 2 {
				if value.Content[j].Tag != "!!int" {
					return fmt.Errorf("resource %s for %q must be an integer", key, value.Content[j-1].Value)
				}
			}
		default:
			return fmt.Errorf("unknown resource setting %q", key)
		}
	}
	*r = ResourceConfig(raw)
	return nil
}

// ValidateResource rejects incomplete budgets and bounds sums used by the
// planner so resource and cost arithmetic cannot wrap around.
func ValidateResource(r *ResourceConfig, models map[string]ModelConfig) error {
	if r == nil {
		return fmt.Errorf("resource configuration is required")
	}
	if r.Capacity <= 0 {
		return fmt.Errorf("capacity must be a positive integer")
	}
	var resources, costs int64
	for id := range models {
		amount, ok := r.Requirements[id]
		if !ok {
			return fmt.Errorf("requirements: missing model %q", id)
		}
		if amount <= 0 {
			return fmt.Errorf("requirements for %q must be a positive integer", id)
		}
		if amount > r.Capacity {
			return fmt.Errorf("requirement for %q (%d) exceeds capacity (%d)", id, amount, r.Capacity)
		}
		if amount > math.MaxInt64-resources {
			return fmt.Errorf("sum of requirements exceeds int64 range")
		}
		resources += amount
		cost := int64(1)
		if configured, ok := r.EvictCosts[id]; ok {
			cost = int64(configured)
		}
		if cost <= 0 {
			return fmt.Errorf("evict_cost for %q must be a positive integer", id)
		}
		if cost > math.MaxInt64-costs {
			return fmt.Errorf("sum of evict_costs exceeds int64 range")
		}
		costs += cost
	}
	for id := range r.Requirements {
		if _, ok := models[id]; !ok {
			return fmt.Errorf("requirements: unknown model %q (use real local model IDs)", id)
		}
	}
	for id := range r.EvictCosts {
		if _, ok := models[id]; !ok {
			return fmt.Errorf("evict_costs: unknown model %q (use real local model IDs)", id)
		}
	}
	return nil
}
