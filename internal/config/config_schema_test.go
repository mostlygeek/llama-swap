package config

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

// TestConfig_ExampleMatchesSchema validates that config.example.yaml conforms to
// config-schema.json. Both files live at the repository root.
func TestConfig_ExampleMatchesSchema(t *testing.T) {
	const (
		schemaPath  = "../../config-schema.json"
		examplePath = "../../config.example.yaml"
	)

	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("reading %s: %v", schemaPath, err)
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
