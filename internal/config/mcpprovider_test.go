package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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
	properties, _ := tools[0].InputSchema["properties"].(map[string]any)
	if _, ok := properties["query"]; !ok {
		t.Errorf("get_config should take a \"query\", schema properties are %v", properties)
	}
	if _, ok := properties["path"]; ok {
		t.Error("get_config should no longer advertise a \"path\"")
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

func TestConfigProvider_GetConfigWithQuery(t *testing.T) {
	got := callConfig(t, newTestConfigProvider(t), `{"query":".models.qwen3"}`).Content

	if !strings.Contains(got, `jq query .models.qwen3`) {
		t.Errorf("result does not name the query:\n%s", got)
	}
	if !strings.Contains(got, "cmd:") || strings.Contains(got, "peers:") {
		t.Errorf("query did not scope the output to the model:\n%s", got)
	}
}

func TestConfigProvider_GetConfigQueryTransforms(t *testing.T) {
	got := callConfig(t, newTestConfigProvider(t), `{"query":".models | keys"}`).Content
	if !strings.Contains(got, "- qwen3") {
		t.Errorf("`.models | keys` did not list the model ids:\n%s", got)
	}

	got = callConfig(t, newTestConfigProvider(t), `{"query":".peers | to_entries | map(.value.proxy)"}`).Content
	if !strings.Contains(got, "http://edge:8080") {
		t.Errorf("a pipeline over peers did not reach the proxy:\n%s", got)
	}
}

// A jq query is only useful if the caller can select values, not just objects,
// and a number has to come back as a number rather than in float notation.
func TestConfigProvider_GetConfigQueryScalarResults(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader("healthCheckTimeout: 300\nmodels:\n  qwen3:\n    cmd: \"server --port ${PORT}\"\n"))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}

	got := callConfig(t, NewConfigProvider(cfg), `{"query":".healthCheckTimeout"}`).Content
	if !strings.Contains(got, "300") || strings.Contains(got, "e+") {
		t.Errorf("want a plain integer result:\n%s", got)
	}
}

// Several results are several YAML documents, so a caller reading them back
// gets a document separator rather than two values run together.
func TestConfigProvider_GetConfigQueryMultipleResults(t *testing.T) {
	got := callConfig(t, newTestConfigProvider(t), `{"query":".models | keys[]"}`).Content
	if !strings.Contains(got, "qwen3") {
		t.Errorf("want the model id in the results:\n%s", got)
	}

	got = callConfig(t, newTestConfigProvider(t), `{"query":"1, 2"}`).Content
	if !strings.Contains(got, "1\n---\n2") {
		t.Errorf("want two YAML documents:\n%s", got)
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

// jq answers a missing key with null rather than an error, so the result has to
// say what null means or the caller reads it as a configured value.
func TestConfigProvider_GetConfigQueryMissingKey(t *testing.T) {
	res := callConfig(t, newTestConfigProvider(t), `{"query":".nowhere"}`)
	if res.IsError {
		t.Errorf("a null result is an answer, not an error:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "null means nothing is set there") {
		t.Errorf("result should explain null:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "Top-level keys:") {
		t.Errorf("result should list valid top-level keys:\n%s", res.Content)
	}
}

func TestConfigProvider_GetConfigQueryMatchesNothing(t *testing.T) {
	res := callConfig(t, newTestConfigProvider(t), `{"query":".models | keys[] | select(. == \"nope\")"}`)
	if res.IsError {
		t.Errorf("an empty result set is an answer, not an error:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "matched nothing") {
		t.Errorf("result should say the query matched nothing:\n%s", res.Content)
	}
}

func TestConfigProvider_GetConfigInvalidQuery(t *testing.T) {
	res := callConfig(t, newTestConfigProvider(t), `{"query":".models | ["}`)
	if !res.IsError {
		t.Fatalf("want IsError for a query that does not parse, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "not a valid jq query") {
		t.Errorf("error should name the problem:\n%s", res.Content)
	}
}

// Model ids usually contain a hyphen, which jq reads as subtraction, so the
// error has to point at the bracket form rather than only saying "invalid".
func TestConfigProvider_GetConfigInvalidQueryHintsAtQuotingIDs(t *testing.T) {
	res := callConfig(t, newTestConfigProvider(t), `{"query":".models.qwen3-8b"}`)
	if !res.IsError {
		t.Fatalf("want IsError for an unquoted hyphenated id, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, `.models["qwen3-8b"]`) {
		t.Errorf("error should show the quoted form:\n%s", res.Content)
	}
}

func TestConfigProvider_GetConfigQueryRuntimeError(t *testing.T) {
	res := callConfig(t, newTestConfigProvider(t), `{"query":".models | length + \"x\""}`)
	if !res.IsError {
		t.Fatalf("want IsError for a query that fails at run time, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "failed") {
		t.Errorf("error should say the query failed:\n%s", res.Content)
	}
}

// The environment holds the credentials redaction exists to keep out of the
// result, so jq's env access must stay closed.
func TestConfigProvider_GetConfigQueryCannotReadEnvironment(t *testing.T) {
	t.Setenv("LLAMA_SWAP_TEST_SECRET", "do-not-leak")

	for _, query := range []string{"env", "$ENV"} {
		res := callConfig(t, newTestConfigProvider(t), fmt.Sprintf(`{"query":%q}`, query))
		if strings.Contains(res.Content, "do-not-leak") || strings.Contains(res.Content, "LLAMA_SWAP_TEST_SECRET") {
			t.Errorf("query %q leaked the process environment:\n%s", query, res.Content)
		}
	}
}

// A query that never terminates has to end with the tool answering, not with
// the request hanging. The deadline comes from the caller's context here so the
// test does not sit out configQueryTimeout.
func TestConfigProvider_GetConfigQueryTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	args := map[string]json.RawMessage{"query": json.RawMessage(`"def f: f; f"`)}
	res, err := newTestConfigProvider(t).Call(ctx, "get_config", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("want IsError for a non-terminating query, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "did not finish") {
		t.Errorf("error should say the query ran out of time:\n%s", res.Content)
	}
}

func TestConfigProvider_GetConfigOversizeResultAsksForANarrowerQuery(t *testing.T) {
	var b strings.Builder
	b.WriteString("models:\n")
	for i := 0; i < 220; i++ {
		fmt.Fprintf(&b, "  model-%03d:\n    cmd: \"llama-server --model /models/model-%03d.gguf --port ${PORT}\"\n", i, i)
	}
	cfg, err := LoadConfigFromReader(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	provider := NewConfigProvider(cfg)

	res := callConfig(t, provider, "")
	if !res.IsError {
		t.Fatalf("expected a large config to be refused, got %d bytes", len(res.Content))
	}
	if res.Truncated || strings.Contains(res.Content, "[truncated]") {
		t.Errorf("an oversize result should be refused, not truncated:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "larger than") || !strings.Contains(res.Content, ".models | keys") {
		t.Errorf("the refusal should suggest a narrower query:\n%s", res.Content)
	}

	// The narrower query the refusal suggests has to actually fit.
	narrowed := callConfig(t, provider, `{"query":".models[\"model-007\"]"}`)
	if narrowed.IsError {
		t.Fatalf("the suggested narrower query failed:\n%s", narrowed.Content)
	}
	if !strings.Contains(narrowed.Content, "model-007.gguf") {
		t.Errorf("narrowed query did not return the model:\n%s", narrowed.Content)
	}
}

// The tool took a dotted path before it took a jq query, so a caller still
// passing one is told the expression that replaces it.
func TestConfigProvider_GetConfigRejectsLegacyPath(t *testing.T) {
	res := callConfig(t, newTestConfigProvider(t), `{"path":"models.qwen3"}`)
	if !res.IsError {
		t.Fatalf("want IsError for the old path argument, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "query=.models.qwen3") {
		t.Errorf("error should name the equivalent query:\n%s", res.Content)
	}

	res = callConfig(t, newTestConfigProvider(t), `{"path":"models.qwen3-8b"}`)
	if !strings.Contains(res.Content, `.models["qwen3-8b"]`) {
		t.Errorf("an id jq cannot take bare should be quoted:\n%s", res.Content)
	}
}

func TestConfigProvider_UnknownTool(t *testing.T) {
	_, err := newTestConfigProvider(t).Call(context.Background(), "get_doc", nil)
	if err == nil {
		t.Fatal("want an error for an unknown tool")
	}
}
