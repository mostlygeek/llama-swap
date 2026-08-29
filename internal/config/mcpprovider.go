package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/mcptools"
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
				"API keys and other credentials redacted. Values are fully resolved: defaults are " +
				"filled in and ${env.*} and macro references are already expanded. " +
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
	yamlText, found, err := p.cfg.RedactedYAML(path)
	if err != nil {
		return mcptools.Result{}, fmt.Errorf("rendering config: %w", err)
	}
	if !found {
		keys := p.cfg.ConfigTopLevelKeys()
		sort.Strings(keys)
		return mcptools.Result{
			IsError: true,
			Content: fmt.Sprintf("Error: no config section at path %q. Top-level keys: %s.",
				path, strings.Join(keys, ", ")),
		}, nil
	}

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
