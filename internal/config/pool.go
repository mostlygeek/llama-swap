package config

import "fmt"

// PoolConfig declares a load-balanced pool of identical-purpose model
// instances. When a request arrives addressed to the pool's name, the server
// picks the least-busy member (ties broken round-robin) and forwards to that
// instance. Members must be regular model IDs declared under top-level
// `models:`, and all members of a pool must live in one shared routing group
// with `swap: false` so they can coexist (validated at load time).
//
// Spillover is the per-member in-flight threshold: while the least-busy warm
// member has fewer than Spillover requests running it keeps receiving
// traffic; at or above it, the picker warms an additional (cold) member.
// Set it to the member's slot count (llama-server -np). Default 1.
type PoolConfig struct {
	Members   []string `yaml:"members"`
	Spillover int      `yaml:"spillover"`
}

func (c *PoolConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawPoolConfig PoolConfig
	defaults := rawPoolConfig{
		Members:   []string{},
		Spillover: 1,
	}
	if err := unmarshal(&defaults); err != nil {
		return err
	}
	if defaults.Spillover == 0 {
		defaults.Spillover = 1
	}
	*c = PoolConfig(defaults)
	return nil
}

// Validate checks the pool is internally consistent. It does not validate
// that members reference existing model IDs or group placement - that is
// done by load.go after the models map and groups are populated.
func (c *PoolConfig) Validate(poolID string) error {
	if len(c.Members) == 0 {
		return fmt.Errorf("pool %q: members must not be empty", poolID)
	}
	if c.Spillover < 1 {
		return fmt.Errorf("pool %q: spillover must be >= 1, got %d", poolID, c.Spillover)
	}
	seen := make(map[string]bool, len(c.Members))
	for _, m := range c.Members {
		if seen[m] {
			return fmt.Errorf("pool %q: duplicate member %q", poolID, m)
		}
		seen[m] = true
	}
	return nil
}
