package config

import "testing"

// TestConfig_ContainerExampleLoads checks that the starter configuration
// shipped inside the unified container image actually loads.
//
// runtime.Dockerfile copies it to /etc/llama-swap/config/config.yaml, which is
// what a container started with nothing mounted runs, so a file that does not
// load there means the image exits immediately on first use.
//
// docs/config.example.yaml is deliberately not covered by this: it documents
// every option, uses strict ${env.X} macros that fail when unset, and is
// reference material rather than something meant to run. TestConfig_ExampleMatchesSchema
// checks that one against the schema instead.
func TestConfig_ContainerExampleLoads(t *testing.T) {
	const configPath = "../../docker/unified/config.example.yaml"

	cfg, err := LoadConfigSources(configPath, "")
	if err != nil {
		t.Fatalf("loading %s: %v", configPath, err)
	}

	if len(cfg.Models) == 0 {
		t.Fatalf("%s defines no models; the image would start with nothing to serve", configPath)
	}
}
