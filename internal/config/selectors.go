package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	SelectorStrategyWarm      = "warm"
	SelectorStrategyPin       = "pin"
	SelectorStrategySpillover = "spillover"
	SelectorStrategyFit       = "fit"
)

// SelectorSettings contains strategy-specific selector settings.
type SelectorSettings struct {
	Spillover int `yaml:"spillover" json:"spillover"`
	// NoEvict makes a fit selector reject the request (429) instead of
	// choosing a target that would stop a running model.
	NoEvict bool `yaml:"no_evict" json:"no_evict"`
}

// SelectorConfig describes a virtual model ID that resolves to a concrete
// local model, alias, or peer model for each request.
type SelectorConfig struct {
	Strategy    string           `yaml:"strategy" json:"strategy"`
	Targets     []string         `yaml:"targets" json:"targets"`
	Settings    SelectorSettings `yaml:"settings" json:"settings"`
	Name        string           `yaml:"name" json:"name"`
	Description string           `yaml:"description" json:"description"`
	Unlisted    bool             `yaml:"unlisted" json:"unlisted"`
	Metadata    map[string]any   `yaml:"metadata" json:"metadata"`
}

// UnmarshalYAML applies selector defaults while retaining the distinction
// between an omitted spillover value and an explicitly invalid zero.
func (c *SelectorConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawSelectorConfig SelectorConfig
	defaults := rawSelectorConfig{
		Targets:  []string{},
		Settings: SelectorSettings{Spillover: 1},
		Metadata: map[string]any{},
	}
	if err := value.Decode(&defaults); err != nil {
		return err
	}
	*c = SelectorConfig(defaults)
	return nil
}

func validateSelectors(config Config) error {
	for selectorID, selector := range config.Selectors {
		if strings.TrimSpace(selectorID) == "" {
			return fmt.Errorf("selectors: selector names cannot be empty")
		}
		if _, found := config.Models[selectorID]; found {
			return fmt.Errorf("selectors.%s: name conflicts with model ID %q", selectorID, selectorID)
		}
		if _, found := config.aliases[selectorID]; found {
			return fmt.Errorf("selectors.%s: name conflicts with model alias %q", selectorID, selectorID)
		}

		switch selector.Strategy {
		case SelectorStrategyPin, SelectorStrategyWarm, SelectorStrategySpillover, SelectorStrategyFit:
		case "":
			return fmt.Errorf("selectors.%s.strategy is required", selectorID)
		default:
			return fmt.Errorf("selectors.%s.strategy: unknown strategy %q (valid: warm, pin, spillover, fit)", selectorID, selector.Strategy)
		}
		if selector.Settings.NoEvict && selector.Strategy != SelectorStrategyFit {
			return fmt.Errorf("selectors.%s.settings.no_evict only applies to strategy %q", selectorID, SelectorStrategyFit)
		}
		if len(selector.Targets) == 0 {
			return fmt.Errorf("selectors.%s.targets must contain at least one entry", selectorID)
		}

		resolvedTargets := make([]string, 0, len(selector.Targets))
		localTargets := make([]string, 0, len(selector.Targets))
		for i, target := range selector.Targets {
			if _, found := config.Selectors[target]; found {
				return fmt.Errorf("selectors.%s.targets[%d] references selector %q; selector chaining is not supported", selectorID, i, target)
			}
			if _, found := config.ResolveBaseModel(target); !found {
				return fmt.Errorf("selectors.%s.targets[%d] references unknown model %q", selectorID, i, target)
			}

			realName, local := config.RealModelName(target)
			if selector.Strategy == SelectorStrategyWarm || selector.Strategy == SelectorStrategyFit {
				if !local {
					return fmt.Errorf("selectors.%s.targets[%d] must resolve to a local model for strategy %q", selectorID, i, selector.Strategy)
				}
			}
			if selector.Strategy == SelectorStrategySpillover {
				if local {
					localTargets = append(localTargets, realName)
				} else {
					peerID, modelID, _ := config.ResolvePeerModel(target)
					realName = PeerModelFQN(peerID, modelID)
				}
				resolvedTargets = append(resolvedTargets, realName)
			}
			if selector.Strategy == SelectorStrategyFit {
				resolvedTargets = append(resolvedTargets, realName)
			}
		}

		if selector.Strategy == SelectorStrategyFit {
			if err := checkDuplicateTargets(selectorID, resolvedTargets); err != nil {
				return err
			}
			continue
		}

		if selector.Strategy != SelectorStrategySpillover {
			continue
		}
		if selector.Settings.Spillover < 1 {
			return fmt.Errorf("selectors.%s.settings.spillover must be >= 1", selectorID)
		}

		if err := checkDuplicateTargets(selectorID, resolvedTargets); err != nil {
			return err
		}
		if err := validateSpilloverCoexistence(config, selectorID, localTargets); err != nil {
			return err
		}
	}
	return nil
}

// SelectorEnabled reports whether a selector can serve requests with the
// configured router. A fit selector needs the capacity router's eviction
// costs; with any other router it is ignored rather than rejected, so a
// config can switch routers without editing its selectors.
func (c Config) SelectorEnabled(selector SelectorConfig) bool {
	return selector.Strategy != SelectorStrategyFit || c.Routing.Router.Use == "capacity"
}

// checkDuplicateTargets rejects targets that resolve to the same model, such as
// a model listed alongside one of its aliases.
func checkDuplicateTargets(selectorID string, resolved []string) error {
	seen := make(map[string]struct{}, len(resolved))
	for _, target := range resolved {
		if _, duplicate := seen[target]; duplicate {
			return fmt.Errorf("selectors.%s.targets contains duplicate resolved model %q", selectorID, target)
		}
		seen[target] = struct{}{}
	}
	return nil
}

// validateSpilloverCoexistence ensures local spillover targets can remain
// resident together under the selected router's constraints.
func validateSpilloverCoexistence(config Config, selectorID string, targets []string) error {
	if len(targets) <= 1 {
		return nil
	}
	if config.Routing.Router.Use == "capacity" {
		capacity := config.Routing.Router.Settings.Capacity
		for pool, total := range capacity.Pools {
			var used int64
			for _, target := range targets {
				used += config.Models[target].Capacities[pool]
			}
			if used > total {
				return fmt.Errorf("selectors.%s spillover targets exceed capacity total in pool %q", selectorID, pool)
			}
		}
		return nil
	}

	if config.Routing.Router.Use == "matrix" {
		matrix := config.Routing.Router.Settings.Matrix
		if matrix != nil && matrix.program != nil && matrix.program.CanContainAll(targets) {
			return nil
		}
		return fmt.Errorf("selectors.%s.targets must all appear together in one expanded matrix set", selectorID)
	}

	groupOf := make(map[string]string, len(config.Models))
	for groupID, group := range config.Routing.Router.Settings.Groups {
		for _, member := range group.Members {
			groupOf[member] = groupID
		}
	}

	groupID := groupOf[targets[0]]
	if groupID == "" {
		return fmt.Errorf("selectors.%s target %q is not in a routing group", selectorID, targets[0])
	}
	if config.Routing.Router.Settings.Groups[groupID].Swap {
		return fmt.Errorf("selectors.%s spillover targets must share a group with swap: false", selectorID)
	}
	for _, target := range targets[1:] {
		if groupOf[target] != groupID {
			return fmt.Errorf("selectors.%s spillover targets must share one routing group", selectorID)
		}
	}
	return nil
}
