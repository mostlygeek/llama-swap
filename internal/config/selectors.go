package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	SelectorStrategyWarm    = "warm"
	SelectorStrategyPin     = "pin"
	SelectorStrategyBalance = "balance"
)

// SelectorBalanceConfig contains settings used by the balance strategy.
type SelectorBalanceConfig struct {
	Spillover int `yaml:"spillover" json:"spillover"`
}

// SelectorConfig describes a virtual model ID that resolves to a concrete
// local model, alias, or peer model for each request.
type SelectorConfig struct {
	Strategy    string                `yaml:"strategy" json:"strategy"`
	Targets     []string              `yaml:"targets" json:"targets"`
	Balance     SelectorBalanceConfig `yaml:"balance" json:"balance"`
	Name        string                `yaml:"name" json:"name"`
	Description string                `yaml:"description" json:"description"`
	Unlisted    bool                  `yaml:"unlisted" json:"unlisted"`
	Metadata    map[string]any        `yaml:"metadata" json:"metadata"`
}

// UnmarshalYAML applies selector defaults while retaining the distinction
// between an omitted spillover value and an explicitly invalid zero.
func (c *SelectorConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawSelectorConfig SelectorConfig
	defaults := rawSelectorConfig{
		Targets:  []string{},
		Balance:  SelectorBalanceConfig{Spillover: 1},
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
		for peerID, peer := range config.Peers {
			for _, modelID := range peer.Models {
				if modelID == selectorID {
					return fmt.Errorf("selectors.%s: name conflicts with peer model %q from peer %q", selectorID, modelID, peerID)
				}
			}
		}

		switch selector.Strategy {
		case SelectorStrategyPin, SelectorStrategyWarm, SelectorStrategyBalance:
		case "":
			return fmt.Errorf("selectors.%s.strategy is required", selectorID)
		default:
			return fmt.Errorf("selectors.%s.strategy: unknown strategy %q (valid: warm, pin, balance)", selectorID, selector.Strategy)
		}
		if len(selector.Targets) == 0 {
			return fmt.Errorf("selectors.%s.targets must contain at least one entry", selectorID)
		}

		resolvedTargets := make([]string, 0, len(selector.Targets))
		for i, target := range selector.Targets {
			if _, found := config.Selectors[target]; found {
				return fmt.Errorf("selectors.%s.targets[%d] references selector %q; selector chaining is not supported", selectorID, i, target)
			}
			if _, found := config.ResolveBaseModel(target); !found {
				return fmt.Errorf("selectors.%s.targets[%d] references unknown model %q", selectorID, i, target)
			}

			if selector.Strategy == SelectorStrategyWarm || selector.Strategy == SelectorStrategyBalance {
				realName, local := config.RealModelName(target)
				if !local {
					return fmt.Errorf("selectors.%s.targets[%d] must resolve to a local model for strategy %q", selectorID, i, selector.Strategy)
				}
				resolvedTargets = append(resolvedTargets, realName)
			}
		}

		if selector.Strategy != SelectorStrategyBalance {
			continue
		}
		if selector.Balance.Spillover < 1 {
			return fmt.Errorf("selectors.%s.balance.spillover must be >= 1", selectorID)
		}

		seen := make(map[string]struct{}, len(resolvedTargets))
		for _, target := range resolvedTargets {
			if _, duplicate := seen[target]; duplicate {
				return fmt.Errorf("selectors.%s.targets contains duplicate resolved model %q", selectorID, target)
			}
			seen[target] = struct{}{}
		}
		if err := validateBalanceCoexistence(config, selectorID, resolvedTargets); err != nil {
			return err
		}
	}
	return nil
}

func validateBalanceCoexistence(config Config, selectorID string, targets []string) error {
	if len(targets) <= 1 {
		return nil
	}

	if config.Routing.Router.Use == "matrix" {
		matrix := config.Routing.Router.Settings.Matrix
		if matrix != nil {
			for _, set := range matrix.ExpandedSets {
				members := make(map[string]struct{}, len(set.Models))
				for _, modelID := range set.Models {
					members[modelID] = struct{}{}
				}
				allFound := true
				for _, target := range targets {
					if _, found := members[target]; !found {
						allFound = false
						break
					}
				}
				if allFound {
					return nil
				}
			}
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
		return fmt.Errorf("selectors.%s balance targets must share a group with swap: false", selectorID)
	}
	for _, target := range targets[1:] {
		if groupOf[target] != groupID {
			return fmt.Errorf("selectors.%s balance targets must share one routing group", selectorID)
		}
	}
	return nil
}
