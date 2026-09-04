package config

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The Docs Agent eval merges this file into the server it starts, and
// evals/docs-agent/cases/configuration/running-config.yaml asserts on the
// values in it. A break here only shows up as a failing eval run against a
// local model, which is slow, stochastic, and easy to blame on the model, so
// the fixture is checked here instead.
const evalFixturePath = "../../evals/docs-agent/fixture/models.yaml"

func loadEvalFixture(t *testing.T) Config {
	t.Helper()
	data, err := os.ReadFile(evalFixturePath)
	if err != nil {
		t.Fatalf("reading %s: %v", evalFixturePath, err)
	}
	cfg, err := LoadConfigFromReader(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("loading %s: %v", evalFixturePath, err)
	}
	return cfg
}

// Every name in the fixture has to be prefixed, because models, groups and
// peers are identity-keyed: a name that also appears in the config of whoever
// runs the eval is a merge error that stops the server from starting.
func TestEvalFixture_NamesAreNamespaced(t *testing.T) {
	cfg := loadEvalFixture(t)

	if len(cfg.Models) < 20 {
		t.Errorf("fixture has %d models; the cases assume a config too long to read whole", len(cfg.Models))
	}
	for id := range cfg.Models {
		if !strings.HasPrefix(id, "eval-") {
			t.Errorf("model %q does not start with \"eval-\" and can collide with a real config", id)
		}
		for _, alias := range cfg.Models[id].Aliases {
			if !strings.HasPrefix(alias, "eval-") {
				t.Errorf("model %q has alias %q that does not start with \"eval-\"", id, alias)
			}
		}
	}
	for name := range cfg.Groups {
		if name == "(default)" {
			continue // synthesized for models that name no group
		}
		if !strings.HasPrefix(name, "eval-") {
			t.Errorf("group %q does not start with \"eval-\" and can collide with a real config", name)
		}
	}
	// A top-level scalar or a global macro merges by value or by name, so
	// either one can conflict with, or silently rewrite, the user's config.
	if len(cfg.Macros) != 0 {
		t.Errorf("fixture defines global macros %v; use model-scoped macros instead", cfg.Macros)
	}
	if len(cfg.Peers) != 0 {
		t.Error("fixture defines peers; nothing in the cases needs them")
	}
}

// The values the eval cases assert on, read back through the tool the agent
// actually calls. Changing the fixture without changing the cases fails here.
func TestEvalFixture_CaseValuesResolve(t *testing.T) {
	provider := NewConfigProvider(loadEvalFixture(t))

	for _, tc := range []struct {
		caseID string
		query  string
		want   []string
	}{
		{"running-config-model-ttl", `.models["eval-llama33-70b"].ttl`, []string{"3600"}},
		{"running-config-strip-params", `.models["eval-devstral-24b"].filters.stripParams`,
			[]string{"temperature", "top_p", "top_k"}},
		{"running-config-capabilities-context", `.models["eval-llama4-scout"].capabilities`,
			[]string{"262144", "image"}},
		{"running-config-nested-metadata", `.models["eval-deepseek-r1-distill-32b"].metadata.reasoning`,
			[]string{"high", "think"}},
		{"running-config-group-members", `.groups["eval-vision"].members`,
			[]string{"eval-gemma3-27b-vision", "eval-llama4-scout", "eval-qwen25-vl-7b"}},
		{"running-config-timeout-field", `.models["eval-mistral-nemo-12b"].timeouts.responseHeader`,
			[]string{"300"}},
		{"running-config-concurrency-across-models",
			`.models | to_entries | map(select(.value.concurrencyLimit != null) | .key)`,
			[]string{"eval-qwen3-coder-30b", "eval-nemotron-49b", "eval-nomic-embed-v15", "eval-qwen3-embed-8b"}},
		{"holdout-running-config-idle-unload", `.models["eval-qwen3-coder-30b"].ttl`, []string{"1800"}},
		{"holdout-running-config-check-endpoint", `.models["eval-phi4-14b"].checkEndpoint`, []string{"/v1/models"}},
		{"holdout-running-config-metadata-quant", `.models["eval-deepseek-v3-671b"].metadata.quant`,
			[]string{"UD-IQ1_S"}},
	} {
		t.Run(tc.caseID, func(t *testing.T) {
			args := map[string]json.RawMessage{"query": mustJSONString(t, tc.query)}
			result, err := provider.Call(context.Background(), "get_config", args)
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if result.IsError {
				t.Fatalf("query %s failed:\n%s", tc.query, result.Content)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.Content, want) {
					t.Errorf("query %s no longer returns %q, which case %s asserts on:\n%s",
						tc.query, want, tc.caseID, result.Content)
				}
			}
		})
	}
}

// The cases are worth running only against a config the agent cannot simply
// read in full. This is the size that makes a jq query the cheaper move.
func TestEvalFixture_IsLongEnoughToNeedAQuery(t *testing.T) {
	provider := NewConfigProvider(loadEvalFixture(t))

	result, err := provider.Call(context.Background(), "get_config", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("the whole fixture no longer renders:\n%s", result.Content)
	}
	if len(result.Content) < 8*1024 {
		t.Errorf("the whole fixture renders to %d bytes; too small for a query to be worth writing",
			len(result.Content))
	}
	// Past the cap the tool refuses instead of answering, which turns every
	// case into a test of error recovery rather than of field extraction.
	if len(result.Content) > maxConfigResultBytes {
		t.Errorf("the whole fixture renders to %d bytes, over the %d byte cap; get_config will refuse it",
			len(result.Content), maxConfigResultBytes)
	}
}

func mustJSONString(t *testing.T, value string) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %q: %v", value, err)
	}
	return json.RawMessage(encoded)
}
