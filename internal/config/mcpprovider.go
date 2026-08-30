package config

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/mcptools"
	"gopkg.in/yaml.v3"
)

// maxConfigResultBytes caps a get_config result. It is larger than the docs
// cap: a config review is a heavier task and the whole point is to see the
// setup, but it is still bounded so a full dump cannot swamp a small context.
const maxConfigResultBytes = 32 * 1024

// ConfigProvider serves the running llama-swap configuration so an agent can
// give advice about the setup in front of it rather than in the abstract.
//
// The configuration is captured by value when the provider is built. A hot
// reload constructs a fresh Server and therefore a fresh registry, so the
// snapshot always matches what is actually running.
type ConfigProvider struct {
	cfg Config
}

// NewConfigProvider builds the provider around a configuration snapshot.
func NewConfigProvider(cfg Config) *ConfigProvider {
	return &ConfigProvider{cfg: cfg}
}

var _ mcptools.Provider = (*ConfigProvider)(nil)

func (p *ConfigProvider) ID() string { return "config" }

func (p *ConfigProvider) Shutdown(time.Duration) error { return nil }

// Reading the configuration is read-only. It is not idempotent across a hot
// reload, but within one process the snapshot never changes.
func configAnnotations() *mcptools.Annotations {
	return &mcptools.Annotations{ReadOnlyHint: true, IdempotentHint: true}
}

func (p *ConfigProvider) Tools(context.Context) ([]mcptools.Tool, error) {
	return []mcptools.Tool{
		{
			Name:  "get_config",
			Title: "Show the current llama-swap configuration",
			Description: "Return the configuration llama-swap is running right now, as YAML, with " +
				"API keys and other credentials redacted. Values are fully resolved: ${env.*} and " +
				"macro references are already expanded, and values equal to their defaults are omitted. " +
				"Pass \"path\" to narrow to a section, for example \"models\" or \"models.qwen3\" or " +
				"\"peers\"; omit it for the whole document. Use this to review or troubleshoot the " +
				"active setup before suggesting changes.",
			Annotations: configAnnotations(),
			Tags:        []string{"config", "configuration", "troubleshooting"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type": "string",
						"description": "Dotted key selecting a section of the config, for example " +
							"'models', 'models.qwen3', 'peers', or 'macros'. Omit for the full config.",
					},
				},
				"additionalProperties": false,
			},
		},
	}, nil
}

func (p *ConfigProvider) Call(_ context.Context, name string, args map[string]json.RawMessage) (mcptools.Result, error) {
	if name != "get_config" {
		return mcptools.Result{}, fmt.Errorf("unknown tool %q", name)
	}

	path := mcptools.StringArg(args, "path")
	// Render the full tree before selecting a path so default-only sections can
	// still be addressed. For example, get_config({"path":"performance"})
	// should return an empty mapping, rather than report that the section does
	// not exist merely because every value in it is a default.
	yamlText, found, err := p.cfg.RedactedYAML("")
	if err != nil {
		return mcptools.Result{}, fmt.Errorf("rendering config: %w", err)
	}
	if !found { // RedactedYAML for the root always succeeds; retain this guard.
		return mcptools.Result{}, fmt.Errorf("rendering config: root configuration was not found")
	}

	var current map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &current); err != nil {
		return mcptools.Result{}, fmt.Errorf("parsing rendered config: %w", err)
	}

	if path != "" {
		_, exists := navigateConfig(current, strings.Split(path, "."))
		if !exists {
			keys := p.cfg.ConfigTopLevelKeys()
			sort.Strings(keys)
			return mcptools.Result{
				IsError: true,
				Content: fmt.Sprintf("Error: no config section at path %q. Top-level keys: %s.",
					path, strings.Join(keys, ", ")),
			}, nil
		}
	}

	defaults, err := configProviderDefaults(p.cfg)
	if err != nil {
		return mcptools.Result{}, fmt.Errorf("building config defaults: %w", err)
	}
	filtered, empty := pruneConfigDefaults(current, defaults, "")
	if empty {
		filtered = map[string]any{}
	}
	if path != "" {
		filtered, _ = navigateConfig(filtered, strings.Split(path, "."))
		if filtered == nil {
			filtered = map[string]any{}
		}
	}

	buf, err := yaml.Marshal(filtered)
	if err != nil {
		return mcptools.Result{}, fmt.Errorf("marshaling filtered config: %w", err)
	}
	yamlText = string(buf)

	var b strings.Builder
	if path == "" {
		b.WriteString("Current llama-swap configuration (credentials redacted, values resolved):\n\n")
	} else {
		fmt.Fprintf(&b, "Current llama-swap configuration at %q (credentials redacted, values resolved):\n\n", path)
	}
	b.WriteString("```yaml\n")
	b.WriteString(strings.TrimRight(yamlText, "\n"))
	b.WriteString("\n```\n")

	// A large config can still exceed the cap even after pruning. Give the
	// model the one move that fixes it rather than leaving it with a
	// half-parsed document.
	result := mcptools.CapResult(b.String(), maxConfigResultBytes)
	if result.Truncated {
		result.Content += "\nThe full config is larger than one response. Call get_config again " +
			"with a \"path\" (for example \"models\", \"models.<id>\", \"peers\", \"groups\") " +
			"to read a section in full."
	}
	return result, nil
}

// configProviderDefaults returns rendered defaults in the same YAML shape as
// RedactedYAML. Defaults for entries in maps (models, peers, selectors, and
// groups) are added by pruneConfigDefaults because an empty config has none.
func configProviderDefaults(cfg Config) (map[string]any, error) {
	base, err := LoadConfigFromReader(strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	yamlText, _, err := base.RedactedYAML("")
	if err != nil {
		return nil, err
	}
	var defaults map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &defaults); err != nil {
		return nil, err
	}

	// Some model defaults inherit the resolved global configuration, so use the
	// active values rather than the empty-config baseline.
	model := ModelConfig{}
	if err := yaml.Unmarshal([]byte("{}"), &model); err != nil {
		return nil, err
	}
	model.HealthCheckTimeout = cfg.HealthCheckTimeout
	model.UnloadAfter = cfg.GlobalTTL
	model.UnloadTimeout = cfg.UnloadTimeout
	sendLoadingState := cfg.SendLoadingState
	model.SendLoadingState = &sendLoadingState
	modelDefaults, err := configProviderYAMLMap(model)
	if err != nil {
		return nil, err
	}
	defaults["__model"] = modelDefaults

	// PeerConfig validates its required fields during unmarshaling. Supply
	// placeholders solely to obtain the defaults, then remove those placeholders.
	peer := PeerConfig{}
	if err := yaml.Unmarshal([]byte("proxy: http://default.invalid\nmodels: [default]\n"), &peer); err != nil {
		return nil, err
	}
	peerDefaults, err := configProviderYAMLMap(peer)
	if err != nil {
		return nil, err
	}
	delete(peerDefaults, "proxy")
	delete(peerDefaults, "models")
	defaults["__peer"] = peerDefaults

	selector := SelectorConfig{}
	if err := yaml.Unmarshal([]byte("{}"), &selector); err != nil {
		return nil, err
	}
	selectorDefaults, err := configProviderYAMLMap(selector)
	if err != nil {
		return nil, err
	}
	defaults["__selector"] = selectorDefaults

	group := GroupConfig{}
	if err := yaml.Unmarshal([]byte("{}"), &group); err != nil {
		return nil, err
	}
	groupDefaults, err := configProviderYAMLMap(group)
	if err != nil {
		return nil, err
	}
	defaults["__group"] = groupDefaults
	return defaults, nil
}

func configProviderYAMLMap(v any) (map[string]any, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// pruneConfigDefaults recursively drops settings equal to their defaults.
// Map-entry defaults need special handling because the empty base config has no
// representative model, peer, selector, or group entry to compare against.
func pruneConfigDefaults(value, defaults any, path string) (any, bool) {
	currentMap, isMap := value.(map[string]any)
	defaultMap, defaultsAreMap := defaults.(map[string]any)
	if !isMap || !defaultsAreMap {
		return value, reflect.DeepEqual(value, defaults)
	}

	out := make(map[string]any, len(currentMap))
	for key, current := range currentMap {
		childPath := key
		if path != "" {
			childPath = path + "." + key
		}

		childDefaults, known := defaultMap[key]
		switch childPath {
		case "models", "peers", "selectors", "groups", "routing.router.settings.groups":
			entryDefaults := defaultMap["__"+strings.TrimSuffix(childPath, "s")]
			if childPath == "routing.router.settings.groups" {
				// The recursive defaults at this path are the routing defaults,
				// not the root defaults where __group is stored.
				entryDefaults = map[string]any{
					"swap":       true,
					"exclusive":  true,
					"persistent": false,
					"members":    []any{},
				}
			}
			entries, ok := current.(map[string]any)
			if !ok {
				out[key] = current
				continue
			}
			prunedEntries := make(map[string]any, len(entries))
			for entryKey, entry := range entries {
				if pruned, empty := pruneConfigDefaults(entry, entryDefaults, childPath+"."+entryKey); !empty {
					prunedEntries[entryKey] = pruned
				}
			}
			if len(prunedEntries) > 0 {
				out[key] = prunedEntries
			}
			continue
		}

		if !known {
			out[key] = current
			continue
		}
		if pruned, empty := pruneConfigDefaults(current, childDefaults, childPath); !empty {
			out[key] = pruned
		}
	}
	return out, len(out) == 0
}
