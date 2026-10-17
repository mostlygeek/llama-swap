package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const resourceModels = `models:
  a: {cmd: echo a, proxy: 'http://localhost:8080', aliases: [alias-a]}
  b: {cmd: echo b, proxy: 'http://localhost:8081'}
`
const resourceRouting = `routing:
  router:
    use: resource
    settings:
      resource:
        capacity: 10
        requirements: {a: 4, b: 6}
        evict_costs: {a: 9}
`

func TestConfig_Resource(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(resourceModels + resourceRouting))
	require.NoError(t, err)
	require.Equal(t, "resource", cfg.Routing.Router.Use)
	require.Equal(t, int64(10), cfg.Routing.Router.Settings.Resource.Capacity)
	require.Empty(t, cfg.Groups)
	require.Empty(t, cfg.Routing.Router.Settings.Groups)
	require.Nil(t, cfg.Matrix)
	require.Nil(t, cfg.Routing.Router.Settings.Matrix)
}

func TestConfig_ResourceValidation(t *testing.T) {
	for _, tc := range []struct{ name, from, to, want string }{
		{"zero capacity", "capacity: 10", "capacity: 0", "capacity must be a positive integer"},
		{"negative capacity", "capacity: 10", "capacity: -1", "capacity must be a positive integer"},
		{"fraction capacity", "capacity: 10", "capacity: 10.5", "capacity must be an integer"},
		{"nonfinite capacity", "capacity: 10", "capacity: .inf", "cannot unmarshal"},
		{"overflow capacity", "capacity: 10", "capacity: 9223372036854775808", "cannot unmarshal"},
		{"zero requirement", "a: 4", "a: 0", "must be a positive integer"},
		{"negative requirement", "a: 4", "a: -1", "must be a positive integer"},
		{"missing all requirements", "requirements: {a: 4, b: 6}", "requirements: {}", "missing model"},
		{"fraction requirement", "a: 4", "a: 4.5", "must be an integer"},
		{"too large", "a: 4", "a: 11", "exceeds capacity"},
		{"missing requirement", "a: 4, b: 6", "a: 4", "missing model"},
		{"unknown requirement", "a: 4, b: 6", "a: 4, b: 6, typo: 1", "unknown model"},
		{"alias requirement", "a: 4, b: 6", "a: 4, b: 6, alias-a: 1", "unknown model"},
		{"zero cost", "a: 9", "a: 0", "must be a positive integer"},
		{"negative cost", "a: 9", "a: -1", "must be a positive integer"},
		{"fraction cost", "a: 9", "a: 1.5", "must be an integer"},
		{"unknown cost", "a: 9", "typo: 9", "unknown model"},
		{"alias cost", "a: 9", "alias-a: 9", "unknown model"},
		{"unknown setting", "capacity: 10", "capacity: 10\n        typo: 1", "unknown resource setting"},
		{"no settings", "      resource:\n        capacity: 10\n        requirements: {a: 4, b: 6}\n        evict_costs: {a: 9}", "", "requires use: resource"},
		{"not selected", "use: resource", "use: group", "requires use: resource"},
		{"implicit group", "    use: resource\n", "", "requires use: resource"},
		{"groups settings", "    settings:\n", "    settings:\n      groups: {}\n", "cannot be combined"},
		{"matrix settings", "    settings:\n", "    settings:\n      matrix: {sets: {s: a}}\n", "cannot be combined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(resourceModels + strings.Replace(resourceRouting, tc.from, tc.to, 1)))
			require.ErrorContains(t, err, tc.want)
		})
	}
	for _, legacy := range []string{"groups: {g: {members: [a]}}", "matrix: {sets: {s: a}}"} {
		_, err := LoadConfigFromReader(strings.NewReader(resourceModels + resourceRouting + legacy))
		require.ErrorContains(t, err, "legacy top-level")
	}
	_, err := LoadConfigFromReader(strings.NewReader(resourceModels + resourceRouting + "groups: {}"))
	require.ErrorContains(t, err, "cannot be combined")
	for _, tc := range []struct {
		requirements map[string]int64
		costs        map[string]int
		want         string
	}{
		{map[string]int64{"a": 9223372036854775807, "b": 1}, nil, "sum of requirements"},
		{map[string]int64{"a": 1, "b": 1}, map[string]int{"a": int(^uint(0) >> 1), "b": int(^uint(0) >> 1)}, "sum of evict_costs"},
	} {
		if tc.want == "sum of evict_costs" && strconv.IntSize < 64 {
			continue // int costs cannot overflow an int64 sum with two models here.
		}
		err := ValidateResource(&ResourceConfig{Capacity: 9223372036854775807, Requirements: tc.requirements, EvictCosts: tc.costs}, makeModels("a", "b"))
		require.ErrorContains(t, err, tc.want)
	}
}

func TestConfig_ResourceConfigSources(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "models.yaml", resourceModels)
	writeYAML(t, dir, "routing.yaml", resourceRouting)
	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	require.Equal(t, "resource", cfg.Routing.Router.Use)
	writeYAML(t, dir, "duplicate.yaml", resourceRouting)
	_, err = LoadConfigSources("", dir)
	require.ErrorContains(t, err, `duplicate resource "capacity"`)
}

func TestConfig_ResourceSpillover(t *testing.T) {
	for _, capacity := range []int{9, 10} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			input := resourceModels + strings.Replace(resourceRouting, "capacity: 10", fmt.Sprintf("capacity: %d", capacity), 1) + `selectors:
  spill:
    strategy: spillover
    targets: [a, b]
`
			_, err := LoadConfigFromReader(strings.NewReader(input))
			if capacity == 10 {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "spillover targets exceed resource capacity")
			}
		})
	}
}

func TestConfig_ResourceSchema(t *testing.T) {
	data, err := os.ReadFile("../../config-schema.json")
	require.NoError(t, err)
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(data, &schema))
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{BaseURI: "https://github.com/mostlygeek/llama-swap/"})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"valid", resourceModels + resourceRouting, true},
		{"missing capacity", resourceModels + strings.Replace(resourceRouting, "        capacity: 10\n", "", 1), false},
		{"zero capacity", resourceModels + strings.Replace(resourceRouting, "capacity: 10", "capacity: 0", 1), false},
		{"fraction", resourceModels + strings.Replace(resourceRouting, "a: 4", "a: 4.5", 1), false},
		{"zero cost", resourceModels + strings.Replace(resourceRouting, "a: 9", "a: 0", 1), false},
		{"missing resource", resourceModels + "routing: {router: {use: resource}}", false},
		{"inactive resource", resourceModels + strings.Replace(resourceRouting, "use: resource", "use: matrix", 1), false},
		{"matrix settings", resourceModels + strings.Replace(resourceRouting, "    settings:\n", "    settings:\n      matrix: {sets: {s: a}}\n", 1), false},
		{"group settings", resourceModels + strings.Replace(resourceRouting, "    settings:\n", "    settings:\n      groups: {}\n", 1), false},
		{"legacy groups", resourceModels + resourceRouting + "groups: {}", false},
		{"legacy matrix", resourceModels + resourceRouting + "matrix: {sets: {s: a}}", false},
		{"default group", resourceModels, true},
		{"legacy group", resourceModels + "groups: {g: {members: [a,b]}}", true},
		{"legacy matrix compatible", resourceModels + "matrix: {vars: {a: a}, sets: {s: a}}", true},
		{"inactive matrix compatible", resourceModels + "routing: {router: {use: group, settings: {groups: {}, matrix: {vars: {a: a}, sets: {s: a}}}}}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var instance any
			require.NoError(t, yaml.Unmarshal([]byte(tc.input), &instance))
			data, err := json.Marshal(instance)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &instance))
			err = resolved.Validate(instance)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
