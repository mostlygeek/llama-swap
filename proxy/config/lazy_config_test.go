package config

import (
	"os"
	"testing"
)

func TestLazyConfig_Merge(t *testing.T) {
	yamlContent := `
proxy: http://127.0.0.1:${PORT}
commands:
  default: vllm run_vllm ${model} false ${PORT}
quant:
  default:
    - Q4_K_M
models:
  - "unsloth/Qwen3.5-27B-GGUF"
`
	tmpfile, err := os.CreateTemp("", "lazy-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(yamlContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	mainConf := &Config{}
	
	err = LoadAndMergeLazyConfig(mainConf, tmpfile.Name())
	if err != nil {
		t.Fatalf("LoadAndMergeLazyConfig failed: %v", err)
	}

	if len(mainConf.Models) == 0 {
		t.Fatal("Expected merged models from lazy config, got none")
	}

	// Wait! unsloth/Qwen3.5-27B-GGUF without matching a type uses default. 
	// The default in the yaml doesn't match a specific quant type since the type string is empty when matching 'default'.
	// But it will still attempt to reach out to HuggingFace or use empty quants rule.
}
