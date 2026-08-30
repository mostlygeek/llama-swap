package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/mcptools"
)

const configProviderFixture = `
apiKeys:
  - "sk-secret-abcdefghijklmnop12345"
models:
  qwen3:
    cmd: "llama-server --model /m/qwen3.gguf --port ${PORT}"
peers:
  edge:
    proxy: "http://edge:8080"
    apiKey: "peer-token-value"
    models: ["m1"]
`

func newTestConfigProvider(t *testing.T) *ConfigProvider {
	t.Helper()
	cfg, err := LoadConfigFromReader(strings.NewReader(configProviderFixture))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	return NewConfigProvider(cfg)
}

func callConfig(t *testing.T, p *ConfigProvider, args string) mcptools.Result {
	t.Helper()
	var parsed map[string]json.RawMessage
	if args != "" {
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			t.Fatalf("bad test args: %v", err)
		}
	}
	result, err := p.Call(context.Background(), "get_config", parsed)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	return result
}

func TestConfigProvider_Tools(t *testing.T) {
	tools, err := newTestConfigProvider(t).Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "get_config" {
		t.Fatalf("tools = %v, want one named %q", tools, "get_config")
	}
	if tools[0].Annotations == nil || !tools[0].Annotations.ReadOnlyHint {
		t.Error("get_config should be marked readOnlyHint")
	}
	if tools[0].InputSchema == nil {
		t.Error("get_config has no inputSchema")
	}
}

func TestConfigProvider_GetConfigRedactsAndFences(t *testing.T) {
	got := callConfig(t, newTestConfigProvider(t), "").Content

	if !strings.Contains(got, "```yaml") {
		t.Errorf("result is not a fenced YAML block:\n%s", got)
	}
	for _, leak := range []string{"sk-secret-abcdefghijklmnop12345", "peer-token-value"} {
		if strings.Contains(got, leak) {
			t.Errorf("result leaked secret %q:\n%s", leak, got)
		}
	}
	if !strings.Contains(got, "/m/qwen3.gguf") {
		t.Errorf("result dropped a non-secret value:\n%s", got)
	}
}

func TestConfigProvider_GetConfigWithPath(t *testing.T) {
	got := callConfig(t, newTestConfigProvider(t), `{"path":"models.qwen3"}`).Content

	if !strings.Contains(got, `at "models.qwen3"`) {
		t.Errorf("result does not name the path:\n%s", got)
	}
	if !strings.Contains(got, "cmd:") || strings.Contains(got, "peers:") {
		t.Errorf("path did not scope the output to the model:\n%s", got)
	}
}

func TestConfigProvider_GetConfigOmitsDefaults(t *testing.T) {
	got := callConfig(t, newTestConfigProvider(t), "").Content

	for _, defaultValue := range []string{
		"healthCheckTimeout: 120",
		"logLevel: info",
		"logToStdout: proxy",
		"metricsMaxInMemory: 1000",
		"checkEndpoint: /health",
		"proxy: http://localhost:${PORT}",
		"connect: 30",
		"responseHeader: 60",
		"swap: true",
		"exclusive: true",
	} {
		if strings.Contains(got, defaultValue) {
			t.Errorf("result includes default value %q:\n%s", defaultValue, got)
		}
	}
	for _, configuredValue := range []string{"cmd:", "proxy: http://edge:8080", "models:", "m1"} {
		if !strings.Contains(got, configuredValue) {
			t.Errorf("result dropped configured value %q:\n%s", configuredValue, got)
		}
	}
}

func TestConfigProvider_GetConfigUnknownPath(t *testing.T) {
	res := callConfig(t, newTestConfigProvider(t), `{"path":"nope.nowhere"}`)
	if !res.IsError {
		t.Fatalf("want IsError for a bad path, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "Top-level keys:") {
		t.Errorf("error should list valid top-level keys:\n%s", res.Content)
	}
}

func TestConfigProvider_GetConfigTruncationPointsAtPath(t *testing.T) {
	var b strings.Builder
	b.WriteString("models:\n")
	for i := 0; i < 220; i++ {
		fmt.Fprintf(&b, "  model-%03d:\n    cmd: \"llama-server --model /models/model-%03d.gguf --port ${PORT}\"\n", i, i)
	}
	cfg, err := LoadConfigFromReader(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}

	got := callConfig(t, NewConfigProvider(cfg), "").Content
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected a large config to truncate, got %d bytes", len(got))
	}
	if !strings.Contains(got, `with a "path"`) {
		t.Errorf("truncated result should tell the caller to use a path:\n%s", got[len(got)-400:])
	}
}

func TestConfigProvider_UnknownTool(t *testing.T) {
	_, err := newTestConfigProvider(t).Call(context.Background(), "get_doc", nil)
	if err == nil {
		t.Fatal("want an error for an unknown tool")
	}
}
