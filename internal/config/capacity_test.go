package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const capacityModels = `models:
  a:
    cmd: echo a
    proxy: http://localhost:8080
    aliases: [alias-a]
    capacities: {ram: 4, gpu0: 2}
    evict_cost: 1
  b:
    cmd: echo b
    proxy: http://localhost:8081
    capacities: {ram: 6}
    evict_cost: 10
`
const capacityRouting = `routing:
  router:
    use: capacity
    settings:
      capacity:
        pools: {ram: 10, gpu0: 8}
`

// TestConfig_Capacity checks pool requirements and zero-usage, zero-cost defaults.
func TestConfig_Capacity(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(capacityModels + capacityRouting))
	require.NoError(t, err)
	require.Equal(t, "capacity", cfg.Routing.Router.Use)
	require.Equal(t, map[string]int64{"ram": 10, "gpu0": 8}, cfg.Routing.Router.Settings.Capacity.Pools)
	require.Equal(t, map[string]int64{"ram": 4, "gpu0": 2}, cfg.Models["a"].Capacities)
	require.Zero(t, cfg.Models["b"].Capacities["gpu0"])
	require.Equal(t, int64(10), cfg.Models["b"].EvictCost)
	require.Empty(t, cfg.Groups)
	require.Empty(t, cfg.Routing.Router.Settings.Groups)
	require.Nil(t, cfg.Matrix)
	require.Nil(t, cfg.Routing.Router.Settings.Matrix)
	for _, declaration := range []string{"", "    capacities: {}\n", "    capacities: {ram: 0}\n", "    evict_cost: 0\n"} {
		input := strings.Replace(capacityModels, "    capacities: {ram: 4, gpu0: 2}\n    evict_cost: 1\n", declaration, 1)
		cfg, err := LoadConfigFromReader(strings.NewReader(input + capacityRouting))
		require.NoError(t, err)
		require.Zero(t, cfg.Models["a"].EvictCost)
		require.Zero(t, cfg.Models["a"].Capacities["ram"])
	}
}

// TestConfig_CapacityValidation covers malformed, impossible, and overflowing
// requirements and conflicts with legacy routing configuration.
func TestConfig_CapacityValidation(t *testing.T) {
	for _, tc := range []struct{ name, from, to, want string }{
		{"zero total", "ram: 10", "ram: 0", "total must be a positive integer"},
		{"negative total", "ram: 10", "ram: -1", "total must be a positive integer"},
		{"missing pools", "pools: {ram: 10, gpu0: 8}", "{}", "at least one resource limit"},
		{"empty pools", "pools: {ram: 10, gpu0: 8}", "pools: {}", "at least one resource limit"},
		{"null pools", "pools: {ram: 10, gpu0: 8}", "pools: null", "must be a mapping"},
		{"array pools", "pools: {ram: 10, gpu0: 8}", "pools: [{name: ram, total: 10}]", "must be a mapping"},
		{"blank pool name", "ram: 10", "' ': 10", "nonempty strings"},
		{"numeric pool name", "ram: 10", "42: 10", "nonempty strings"},
		{"fraction total", "ram: 10", "ram: 10.5", "must be an integer"},
		{"nonfinite total", "ram: 10", "ram: .inf", "must be an integer"},
		{"overflow total", "ram: 10", "ram: 9223372036854775808", "cannot unmarshal"},
		{"duplicate pool", "gpu0: 8", "ram: 8", "already defined"},
		{"negative usage", "ram: 4", "ram: -1", "must be a nonnegative integer"},
		{"fraction usage", "ram: 4", "ram: 4.5", "must be an integer"},
		{"quoted usage", "ram: 4", "ram: '4'", "must be an integer"},
		{"null usage", "ram: 4", "ram: null", "must be an integer"},
		{"overflow usage", "ram: 4", "ram: 9223372036854775808", "cannot unmarshal"},
		{"oversized model", "ram: 4", "ram: 11", "exceeds total"},
		{"unknown pool", "ram: 4", "typo: 4", "unknown capacity pool"},
		{"unknown zero pool", "ram: 4", "typo: 0", "unknown capacity pool"},
		{"blank requirement", "ram: 4", "' ': 4", "nonempty strings"},
		{"numeric requirement", "ram: 4", "42: 4", "nonempty strings"},
		{"duplicate requirement", "ram: 4, gpu0: 2", "ram: 4, ram: 2", "already defined"},
		{"null capacities", "capacities: {ram: 4, gpu0: 2}", "capacities: null", "must be a mapping"},
		{"array capacities", "capacities: {ram: 4, gpu0: 2}", "capacities: [4]", "must be a mapping"},
		{"negative cost", "evict_cost: 1\n", "evict_cost: -1\n", "must be a nonnegative integer"},
		{"fraction cost", "evict_cost: 1\n", "evict_cost: 1.5\n", "must be an integer"},
		{"quoted cost", "evict_cost: 1\n", "evict_cost: '1'\n", "must be an integer"},
		{"null cost", "evict_cost: 1\n", "evict_cost: null\n", "must be an integer"},
		{"overflow cost", "evict_cost: 1\n", "evict_cost: 9223372036854775808\n", "cannot unmarshal"},
		{"unknown router setting", "pools: {ram: 10, gpu0: 8}", "total: 10", "unknown capacity setting"},
		{"unknown router", "use: capacity", "use: resource", "unknown router"},
		{"no settings", "    settings:\n      capacity:\n        pools: {ram: 10, gpu0: 8}\n", "", "capacity configuration is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(strings.Replace(capacityModels+capacityRouting, tc.from, tc.to, 1)))
			require.ErrorContains(t, err, tc.want)
		})
	}
	for _, legacy := range []string{"groups: {g: {members: [a]}}", "matrix: {sets: {s: a}}"} {
		_, err := LoadConfigFromReader(strings.NewReader(capacityModels + capacityRouting + legacy))
		require.ErrorContains(t, err, "legacy top-level")
	}
	_, err := LoadConfigFromReader(strings.NewReader(capacityModels + capacityRouting + "groups: {}"))
	require.ErrorContains(t, err, "legacy top-level")
	err = ValidateCapacity(&CapacityConfig{Pools: map[string]int64{"ram": math.MaxInt64}}, map[string]ModelConfig{
		"a": {Capacities: map[string]int64{"ram": math.MaxInt64}}, "b": {Capacities: map[string]int64{"ram": 1}},
	})
	require.ErrorContains(t, err, "sum of capacities")
	_, err = LoadConfigFromReader(strings.NewReader(strings.Replace(capacityModels, "evict_cost: 1\n", "evict_cost: 9223372036854775807\n", 1) + capacityRouting))
	require.ErrorContains(t, err, "sum of evict_cost")
}

// TestConfig_CapacityConfigSources checks pool merging across configuration
// files and rejects duplicate pool identities even when their limits agree.
func TestConfig_CapacityConfigSources(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "models.yaml", capacityModels)
	writeYAML(t, dir, "routing.yaml", capacityRouting)
	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	require.Equal(t, "capacity", cfg.Routing.Router.Use)
	// Pools can be declared in separate files, but duplicate identities fail
	// even when their limits agree.
	writeYAML(t, dir, "extra.yaml", strings.Replace(capacityRouting, "{ram: 10, gpu0: 8}", "{gpu1: 16}", 1))
	cfg, err = LoadConfigSources("", dir)
	require.NoError(t, err)
	require.Equal(t, int64(16), cfg.Routing.Router.Settings.Capacity.Pools["gpu1"])
	writeYAML(t, dir, "duplicate.yaml", capacityRouting)
	_, err = LoadConfigSources("", dir)
	require.ErrorContains(t, err, `duplicate pools`)
}

// TestConfig_CapacitySpillover checks coexistence against each resource pool,
// treating undeclared model requirements as zero usage.
func TestConfig_CapacitySpillover(t *testing.T) {
	for _, pool := range []string{"ram", "gpu0"} {
		for _, total := range []int{9, 10} {
			t.Run(fmt.Sprintf("%s/%d", pool, total), func(t *testing.T) {
				models := strings.ReplaceAll(capacityModels, "{ram: 4, gpu0: 2}", "{"+pool+": 4}")
				models = strings.ReplaceAll(models, "{ram: 6}", "{"+pool+": 6}")
				routing := strings.Replace(capacityRouting, "{ram: 10, gpu0: 8}", fmt.Sprintf("{%s: %d}", pool, total), 1)
				input := models + routing + "selectors:\n  spill:\n    strategy: spillover\n    targets: [a, b]\n"
				_, err := LoadConfigFromReader(strings.NewReader(input))
				if total == 10 {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "spillover targets exceed capacity")
				}
			})
		}
	}
	input := strings.Replace(capacityModels, "    capacities: {ram: 4, gpu0: 2}\n", "", 1) + capacityRouting + "selectors:\n  spill:\n    strategy: spillover\n    targets: [a, b]\n"
	_, err := LoadConfigFromReader(strings.NewReader(input))
	require.NoError(t, err, "undeclared requirements consume zero")
}

// TestConfig_CapacitySchema checks schema constraints and verifies valid
// examples also load, including configurations with inactive router settings.
func TestConfig_CapacitySchema(t *testing.T) {
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
		{"valid", capacityModels + capacityRouting, true},
		{"missing pools", capacityModels + strings.Replace(capacityRouting, "pools: {ram: 10, gpu0: 8}", "{}", 1), false},
		{"empty pools", capacityModels + strings.Replace(capacityRouting, "{ram: 10, gpu0: 8}", "{}", 1), false},
		{"zero total", capacityModels + strings.Replace(capacityRouting, "ram: 10", "ram: 0", 1), false},
		{"blank name", capacityModels + strings.Replace(capacityRouting, "ram: 10", "' ': 10", 1), false},
		{"fraction usage", strings.Replace(capacityModels, "ram: 4", "ram: 4.5", 1) + capacityRouting, false},
		{"negative usage", strings.Replace(capacityModels, "ram: 4", "ram: -4", 1) + capacityRouting, false},
		{"negative cost", strings.Replace(capacityModels, "evict_cost: 1\n", "evict_cost: -1\n", 1) + capacityRouting, false},
		{"zero cost", strings.Replace(capacityModels, "evict_cost: 1\n", "evict_cost: 0\n", 1) + capacityRouting, true},
		{"empty requirements", strings.Replace(capacityModels, "{ram: 4, gpu0: 2}", "{}", 1) + capacityRouting, true},
		{"missing requirements", strings.Replace(capacityModels, "    capacities: {ram: 4, gpu0: 2}\n", "", 1) + capacityRouting, true},
		{"default cost", strings.Replace(capacityModels, "    evict_cost: 1\n", "", 1) + capacityRouting, true},
		{"missing capacity settings", capacityModels + "routing: {router: {use: capacity}}", false},
		{"inactive capacity", capacityModels + strings.Replace(capacityRouting, "use: capacity", "use: group", 1), true},
		{"implicit group", capacityModels + strings.Replace(capacityRouting, "    use: capacity\n", "", 1), true},
		{"inactive matrix", capacityModels + strings.Replace(capacityRouting, "    settings:\n", "    settings:\n      matrix: {vars: {a: a}, sets: {s: a}}\n", 1), true},
		{"inactive groups", capacityModels + strings.Replace(capacityRouting, "    settings:\n", "    settings:\n      groups: {}\n", 1), true},
		{"matrix with inactive capacity", capacityModels + strings.Replace(strings.Replace(capacityRouting, "use: capacity", "use: matrix", 1), "    settings:\n", "    settings:\n      matrix: {vars: {a: a}, sets: {s: 'a & b'}}\n", 1), true},
		{"legacy groups", capacityModels + capacityRouting + "groups: {}", false},
		{"legacy matrix", capacityModels + capacityRouting + "matrix: {vars: {a: a}, sets: {s: a}}", false},
		{"default group", capacityModels, true},
		{"legacy group", capacityModels + "groups: {g: {members: [a,b]}}", true},
		{"legacy matrix compatible", capacityModels + "matrix: {vars: {a: a}, sets: {s: a}}", true},
		{"inactive matrix compatible", capacityModels + "routing: {router: {use: group, settings: {groups: {}, matrix: {vars: {a: a}, sets: {s: a}}}}}", true},
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
				_, err = LoadConfigFromReader(strings.NewReader(tc.input))
				require.NoError(t, err, "schema and config loader must both accept the example")
			} else {
				require.Error(t, err)
			}
		})
	}
}

// TestConfig_CapacityGuideExamples keeps the routing guide's YAML examples
// compatible with the loader on both Unix and Windows checkouts.
func TestConfig_CapacityGuideExamples(t *testing.T) {
	data, err := os.ReadFile("../../docs/kb/guides/routing/groups-and-matrix.md")
	require.NoError(t, err)
	_, section, found := strings.Cut(strings.ReplaceAll(string(data), "\r\n", "\n"), "## `capacity`")
	require.True(t, found)
	section = strings.SplitN(section, "## Which one?", 2)[0]
	examples := strings.Split(section, "```yaml\n")[1:]
	require.NotEmpty(t, examples)
	for _, example := range examples {
		input := strings.SplitN(example, "```", 2)[0]
		cfg, err := LoadConfigFromReader(strings.NewReader(input))
		require.NoError(t, err)
		require.Equal(t, "capacity", cfg.Routing.Router.Use)
	}
}
