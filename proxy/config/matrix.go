package config

import (
	"fmt"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

var aliasKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9]{1,8}$`)

// MatrixConfig represents the swap matrix configuration block.
type MatrixConfig struct {
	Aliases    map[string]string `yaml:"aliases"`
	EvictCosts map[string]int    `yaml:"evict_costs"`
	Sets       OrderedSets       `yaml:"sets"`
}

// SetEntry is a single named set with its DSL expression.
type SetEntry struct {
	Name string
	DSL  string
}

// OrderedSets preserves YAML definition order of sets (used for tie-breaking).
type OrderedSets []SetEntry

func (os *OrderedSets) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("sets must be a mapping")
	}

	entries := make([]SetEntry, 0, len(value.Content)/2)
	for i := 0; i < len(value.Content); i += 2 {
		keyNode := value.Content[i]
		valueNode := value.Content[i+1]

		var name string
		if err := keyNode.Decode(&name); err != nil {
			return fmt.Errorf("failed to decode set name: %w", err)
		}

		var dsl string
		if err := valueNode.Decode(&dsl); err != nil {
			return fmt.Errorf("failed to decode DSL for set %q: %w", name, err)
		}

		entries = append(entries, SetEntry{Name: name, DSL: dsl})
	}

	*os = entries
	return nil
}

// ExpandedSet is one valid combination of concurrent models (real model names).
type ExpandedSet struct {
	SetName string
	Models  []string // real model names, sorted
}

// ValidateMatrix validates the matrix config and returns all expanded sets.
func ValidateMatrix(matrix MatrixConfig, models map[string]ModelConfig) ([]ExpandedSet, error) {
	if len(matrix.Sets) == 0 {
		return nil, fmt.Errorf("matrix must define at least one set")
	}

	// Build alias resolver: alias -> real model name
	aliasToModel := make(map[string]string)
	if matrix.Aliases != nil {
		for alias, modelName := range matrix.Aliases {
			if !aliasKeyPattern.MatchString(alias) {
				return nil, fmt.Errorf("alias %q must be alphanumeric and 1-8 characters", alias)
			}
			if _, exists := models[alias]; exists {
				return nil, fmt.Errorf("alias %q collides with a model name", alias)
			}
			if _, exists := models[modelName]; !exists {
				return nil, fmt.Errorf("alias %q references unknown model %q", alias, modelName)
			}
			aliasToModel[alias] = modelName
		}
	}

	// resolveIdentifier maps an alias or model name to the real model name
	resolveIdentifier := func(name string) (string, error) {
		if realName, ok := aliasToModel[name]; ok {
			return realName, nil
		}
		if _, ok := models[name]; ok {
			return name, nil
		}
		return "", fmt.Errorf("unknown model or alias %q", name)
	}

	// Validate evict_costs
	if matrix.EvictCosts != nil {
		for key, cost := range matrix.EvictCosts {
			if cost <= 0 {
				return nil, fmt.Errorf("evict_cost for %q must be a positive integer, got %d", key, cost)
			}
			if _, err := resolveIdentifier(key); err != nil {
				return nil, fmt.Errorf("evict_costs: %w", err)
			}
		}
	}

	// Build dependency graph for +ref topological sort
	setNames := make(map[string]bool)
	for _, entry := range matrix.Sets {
		setNames[entry.Name] = true
	}

	deps := make(map[string][]string) // setName -> set names it depends on
	for _, entry := range matrix.Sets {
		refs, err := extractRefs(entry.DSL)
		if err != nil {
			return nil, fmt.Errorf("set %q: %w", entry.Name, err)
		}
		for _, ref := range refs {
			if !setNames[ref] {
				return nil, fmt.Errorf("set %q references undefined set %q", entry.Name, ref)
			}
		}
		deps[entry.Name] = refs
	}

	// Topological sort with cycle detection
	order, err := topologicalSort(matrix.Sets, deps)
	if err != nil {
		return nil, err
	}

	// Expand sets in topological order
	resolvedRefs := make(map[string][][]string) // set name -> expanded alias-level combos
	var allExpanded []ExpandedSet
	totalCombinations := 0

	// Build ordered map for efficient lookup
	setDSL := make(map[string]string)
	for _, entry := range matrix.Sets {
		setDSL[entry.Name] = entry.DSL
	}

	for _, name := range order {
		dsl := setDSL[name]
		combos, err := ParseAndExpandDSL(dsl, resolvedRefs)
		if err != nil {
			return nil, fmt.Errorf("set %q: %w", name, err)
		}

		resolvedRefs[name] = combos

		// Resolve aliases to real model names
		for _, combo := range combos {
			resolved := make([]string, len(combo))
			for i, ident := range combo {
				realName, err := resolveIdentifier(ident)
				if err != nil {
					return nil, fmt.Errorf("set %q: %w", name, err)
				}
				resolved[i] = realName
			}
			sort.Strings(resolved)
			allExpanded = append(allExpanded, ExpandedSet{
				SetName: name,
				Models:  resolved,
			})
		}

		totalCombinations += len(combos)
		if totalCombinations > maxDSLExpansions {
			return nil, fmt.Errorf("total expanded combinations (%d) exceed limit of %d", totalCombinations, maxDSLExpansions)
		}
	}

	return allExpanded, nil
}

// topologicalSort returns set names in dependency order.
// Returns an error if a cycle is detected.
func topologicalSort(sets OrderedSets, deps map[string][]string) ([]string, error) {
	// States: 0 = unvisited, 1 = visiting, 2 = visited
	state := make(map[string]int)
	var order []string

	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("circular reference detected involving set %q", name)
		case 2:
			return nil
		}
		state[name] = 1

		for _, dep := range deps[name] {
			if err := visit(dep); err != nil {
				return err
			}
		}

		state[name] = 2
		order = append(order, name)
		return nil
	}

	// Visit in definition order for deterministic output
	for _, entry := range sets {
		if state[entry.Name] == 0 {
			if err := visit(entry.Name); err != nil {
				return nil, err
			}
		}
	}

	return order, nil
}

// ResolvedEvictCosts returns a map of real model name -> evict cost,
// resolving any aliases. Models not listed default to 1.
func (m *MatrixConfig) ResolvedEvictCosts() map[string]int {
	costs := make(map[string]int)
	if m.EvictCosts == nil {
		return costs
	}
	for key, cost := range m.EvictCosts {
		// Resolve alias if present
		if realName, ok := m.Aliases[key]; ok {
			costs[realName] = cost
		} else {
			costs[key] = cost
		}
	}
	return costs
}
