package config

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

var varKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9]{1,8}$`)

// MatrixConfig represents the swap matrix configuration block.
type MatrixConfig struct {
	Var        map[string]string `yaml:"vars"`
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
	DSL     string
	Models  []string // real model names, sorted
}

// ValidateMatrix validates the matrix config and returns all expanded sets.
func ValidateMatrix(matrix MatrixConfig, models map[string]ModelConfig) ([]ExpandedSet, error) {
	if len(matrix.Sets) == 0 {
		return nil, fmt.Errorf("matrix must define at least one set")
	}

	if err := validateMatrixVars(matrix, models); err != nil {
		return nil, err
	}

	if err := validateEvictCosts(matrix, models); err != nil {
		return nil, err
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
	resolvedRefs := make(map[string][][]string) // set name -> expanded identifier combos
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

		// Resolve identifiers to real model names
		for _, combo := range combos {
			resolved, err := resolveMatrixCombo(matrix, models, combo)
			if err != nil {
				return nil, fmt.Errorf("set %q: %w", name, err)
			}
			allExpanded = append(allExpanded, ExpandedSet{
				SetName: name,
				DSL:     dsl,
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

func validateMatrixVars(matrix MatrixConfig, models map[string]ModelConfig) error {
	for id, modelName := range matrix.Var {
		if !varKeyPattern.MatchString(id) {
			return fmt.Errorf("var key %q must be alphanumeric and 1-8 characters", id)
		}
		if _, exists := models[id]; exists {
			return fmt.Errorf("var key %q conflicts with model id %q", id, id)
		}
		if _, exists := models[modelName]; !exists {
			return fmt.Errorf("var key %q references unknown model %q", id, modelName)
		}
	}
	return nil
}

func resolveMatrixCombo(matrix MatrixConfig, models map[string]ModelConfig, combo []string) ([]string, error) {
	resolved := make([]string, len(combo))
	for i, ident := range combo {
		realName, ok := resolveMatrixIdent(matrix, models, ident)
		if !ok {
			return nil, fmt.Errorf("unknown model ID %q", ident)
		}
		resolved[i] = realName
	}
	return dedupAndSort(resolved), nil
}

func resolveMatrixIdent(matrix MatrixConfig, models map[string]ModelConfig, ident string) (string, bool) {
	if realName, ok := matrix.Var[ident]; ok {
		return realName, true
	}
	if _, ok := models[ident]; ok {
		return ident, true
	}
	return "", false
}

func validateEvictCosts(matrix MatrixConfig, models map[string]ModelConfig) error {
	if matrix.EvictCosts == nil {
		return nil
	}

	sources := make(map[string]string, len(matrix.EvictCosts))
	for key, cost := range matrix.EvictCosts {
		if cost <= 0 {
			return fmt.Errorf("evict_cost for %q must be a positive integer, got %d", key, cost)
		}
		realName, ok := resolveMatrixIdent(matrix, models, key)
		if !ok {
			return fmt.Errorf("evict_costs: unknown model ID %q", key)
		}
		if previous, exists := sources[realName]; exists {
			return fmt.Errorf("evict_costs: %q and %q both resolve to model %q", previous, key, realName)
		}
		sources[realName] = key
	}
	return nil
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
// resolving var IDs. Models not listed default to 1.
func (m *MatrixConfig) ResolvedEvictCosts() map[string]int {
	costs := make(map[string]int)
	if m.EvictCosts == nil {
		return costs
	}
	for key, cost := range m.EvictCosts {
		if realName, ok := m.Var[key]; ok {
			costs[realName] = cost
		} else {
			costs[key] = cost
		}
	}
	return costs
}
