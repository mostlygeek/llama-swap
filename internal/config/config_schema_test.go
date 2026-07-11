package config

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

func resolveConfigSchema(t *testing.T) *jsonschema.Resolved {
	t.Helper()
	schemaBytes, err := os.ReadFile("../../config-schema.json")
	if err != nil {
		t.Fatalf("reading config-schema.json: %v", err)
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("unmarshalling schema: %v", err)
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{
		BaseURI: "https://github.com/mostlygeek/llama-swap/",
	})
	if err != nil {
		t.Fatalf("resolving schema: %v", err)
	}
	return resolved
}

// TestConfig_ExampleMatchesSchema validates that config.example.yaml conforms to
// config-schema.json. Both files live at the repository root.
func TestConfig_ExampleMatchesSchema(t *testing.T) {
	const examplePath = "../../config.example.yaml"

	resolved := resolveConfigSchema(t)

	exampleBytes, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("reading %s: %v", examplePath, err)
	}

	// Convert YAML to a JSON-like value so numbers and keys match what the
	// validator expects.
	var yamlValue any
	if err := yaml.Unmarshal(exampleBytes, &yamlValue); err != nil {
		t.Fatalf("unmarshalling example yaml: %v", err)
	}
	jsonBytes, err := json.Marshal(yamlValue)
	if err != nil {
		t.Fatalf("converting example to json: %v", err)
	}
	var instance any
	if err := json.Unmarshal(jsonBytes, &instance); err != nil {
		t.Fatalf("unmarshalling example json: %v", err)
	}

	if err := resolved.Validate(instance); err != nil {
		t.Fatalf("config.example.yaml does not match config-schema.json:\n%v", err)
	}
}

func TestConfig_SchemaReasoningRequiresOutputLimit(t *testing.T) {
	resolved := resolveConfigSchema(t)

	withoutLimit := map[string]any{
		"models": map[string]any{
			"m": map[string]any{
				"cmd": "llama-server",
				"capabilities": map[string]any{
					"reasoning": map[string]any{
						"default": "low",
						"efforts": map[string]any{"low": 1},
					},
				},
			},
		},
	}
	if err := resolved.Validate(withoutLimit); err == nil {
		t.Fatal("reasoning without max_output_tokens passed schema validation")
	}

	withLimit := map[string]any{
		"models": map[string]any{
			"m": map[string]any{
				"cmd": "llama-server",
				"capabilities": map[string]any{
					"max_output_tokens": 2,
					"reasoning": map[string]any{
						"default": "low",
						"efforts": map[string]any{"low": 1},
					},
				},
			},
		},
	}
	if err := resolved.Validate(withLimit); err != nil {
		t.Fatalf("reasoning with max_output_tokens failed schema validation: %v", err)
	}
}
