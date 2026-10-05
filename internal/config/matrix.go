package config

import (
	"fmt"
	"regexp"

	matrixdsl "github.com/mostlygeek/llama-swap/internal/matrix"
	"gopkg.in/yaml.v3"
)

var varKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9.-]{1,32}$`)

// Reclaim selects the matrix solver's eviction objective. The values mirror
// matrixdsl.ReclaimMinimal/ReclaimQueue.
const (
	// ReclaimMinimal minimises total eviction cost (the historical
	// objective); models not in the pending queue are still costed.
	ReclaimMinimal = matrixdsl.ReclaimMinimal
	// ReclaimQueue charges eviction cost only for models the pending
	// request queue references, so idle unqueued models turn over first
	// and a model the queue still references is protected while a backlog
	// exists.
	ReclaimQueue = matrixdsl.ReclaimQueue
)

// MatrixConfig represents the swap matrix configuration block.
type MatrixConfig struct {
	Var        map[string]string `yaml:"vars"`
	EvictCosts map[string]int    `yaml:"evict_costs"`
	Sets       OrderedSets       `yaml:"sets"`
	// Reclaim is normalized to a non-empty value by ValidateMatrix; empty
	// means ReclaimMinimal.
	Reclaim string `yaml:"reclaim"`

	program *matrixdsl.Program
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

// ValidateMatrix validates and compiles a matrix configuration.
func ValidateMatrix(matrix *MatrixConfig, models map[string]ModelConfig) error {
	if len(matrix.Sets) == 0 {
		return fmt.Errorf("matrix must define at least one set")
	}

	// Normalize the reclaim objective so consumers never see the empty
	// string.
	switch matrix.Reclaim {
	case "":
		matrix.Reclaim = ReclaimMinimal
	case ReclaimMinimal, ReclaimQueue:
	default:
		return fmt.Errorf("reclaim must be %q or %q, got %q", ReclaimMinimal, ReclaimQueue, matrix.Reclaim)
	}

	// Validate var entries
	if matrix.Var != nil {
		for id, modelName := range matrix.Var {
			if !varKeyPattern.MatchString(id) {
				return fmt.Errorf("var key %q must contain only alphanumeric, '-' or '.' characters and be 1-32 characters long", id)
			}
			if _, exists := models[modelName]; !exists {
				return fmt.Errorf("var key %q references unknown model %q", id, modelName)
			}
		}
	}

	// Validate evict_costs
	if matrix.EvictCosts != nil {
		for key, cost := range matrix.EvictCosts {
			if cost <= 0 {
				return fmt.Errorf("evict_cost multiplier for %q must be a positive integer, got %d", key, cost)
			}
			if _, ok := resolveMatrixModel(key, matrix.Var, models); !ok {
				return fmt.Errorf("evict_costs: unknown var or model %q", key)
			}
		}
	}

	definitions := make([]matrixdsl.Definition, 0, len(matrix.Sets))
	for _, entry := range matrix.Sets {
		definitions = append(definitions, matrixdsl.Definition{
			Name: entry.Name,
			DSL:  entry.DSL,
		})
	}

	program, err := matrixdsl.Compile(definitions, func(ident string) (string, bool) {
		return resolveMatrixModel(ident, matrix.Var, models)
	})
	if err != nil {
		return err
	}
	matrix.program = program
	return nil
}

func resolveMatrixModel(ident string, vars map[string]string, models map[string]ModelConfig) (string, bool) {
	if modelName, ok := vars[ident]; ok {
		return modelName, true
	}
	if _, ok := models[ident]; ok {
		return ident, true
	}
	return "", false
}

// ResolvedEvictCosts returns a map of real model name -> evict cost
// multiplier, resolving var IDs. The multiplier scales the model's measured
// median load time when the router costs an eviction. Models not listed
// default to 1.
func (m *MatrixConfig) ResolvedEvictCosts() map[string]int {
	costs := make(map[string]int)
	if m.EvictCosts == nil {
		return costs
	}
	for key, cost := range m.EvictCosts {
		// Resolve var ID if present
		if realName, ok := m.Var[key]; ok {
			costs[realName] = cost
		} else {
			costs[key] = cost
		}
	}
	return costs
}

// Program returns the immutable compiled matrix program.
func (m *MatrixConfig) Program() *matrixdsl.Program {
	return m.program
}
