package config

import (
	"strings"
	"testing"
)

// redactFixture is a config that exercises every redaction path: a top-level
// key list, a nested credential key, a secret inside a cmd string, a secret
// env value next to a harmless one, and a macro whose value is a known token
// format even though its name is innocent.
const redactFixture = `
apiKeys:
  - "sk-frontdoorkey1234567890abcd"
macros:
  hftoken: "hf_AAAAAAAAAAAAAAAAAAAAAAAA"
  modelpath: "/models/qwen3.gguf"
models:
  qwen3:
    cmd: |
      llama-server --model ${modelpath} --api-key sk-inline1234567890abcdef --port ${PORT}
    env:
      - "HF_TOKEN=hf_BBBBBBBBBBBBBBBBBBBBBBBB"
      - "CUDA_VISIBLE_DEVICES=1"
peers:
  upstream1:
    proxy: "http://10.0.0.5:8080"
    apiKey: "peer-shared-secret"
    models: ["remote-model"]
`

func loadRedactFixture(t *testing.T) Config {
	t.Helper()
	cfg, err := LoadConfigFromReader(strings.NewReader(redactFixture))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	return cfg
}

func TestConfig_RedactedYAML_RemovesEveryCredential(t *testing.T) {
	out, found, err := loadRedactFixture(t).RedactedYAML("")
	if err != nil || !found {
		t.Fatalf("RedactedYAML: found=%v err=%v", found, err)
	}

	for _, leak := range []string{
		"sk-frontdoorkey1234567890abcd",
		"sk-inline1234567890abcdef",
		"hf_AAAAAAAAAAAAAAAAAAAAAAAA",
		"hf_BBBBBBBBBBBBBBBBBBBBBBBB",
		"peer-shared-secret",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("redacted config still contains secret %q:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, RedactedPlaceholder) {
		t.Errorf("nothing was redacted:\n%s", out)
	}
}

func TestConfig_RedactedYAML_KeepsHarmlessValues(t *testing.T) {
	out, _, err := loadRedactFixture(t).RedactedYAML("")
	if err != nil {
		t.Fatalf("RedactedYAML: %v", err)
	}

	for _, keep := range []string{
		"/models/qwen3.gguf",     // resolved macro value, not a secret
		"CUDA_VISIBLE_DEVICES=1", // non-secret env entry survives intact
		"http://10.0.0.5:8080",   // peer proxy URL is not a credential
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("redacted config dropped harmless value %q:\n%s", keep, out)
		}
	}
}

func TestConfig_RedactedYAML_RedactsCmdFlagButKeepsTheRest(t *testing.T) {
	out, _, err := loadRedactFixture(t).RedactedYAML("models.qwen3")
	if err != nil {
		t.Fatalf("RedactedYAML: %v", err)
	}
	if !strings.Contains(out, "--api-key "+RedactedPlaceholder) {
		t.Errorf("cmd api-key flag not redacted:\n%s", out)
	}
	if !strings.Contains(out, "llama-server --model /models/qwen3.gguf") {
		t.Errorf("cmd was over-redacted, the command itself is gone:\n%s", out)
	}
}

func TestConfig_RedactedYAML_RedactsInlineAuthHeader(t *testing.T) {
	const yml = `
models:
  m:
    cmd: |
      proxy-wrapper --header "Authorization: Bearer sk-headertoken1234567890abcd" --port ${PORT}
`
	cfg, err := LoadConfigFromReader(strings.NewReader(yml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out, _, err := cfg.RedactedYAML("models.m")
	if err != nil {
		t.Fatalf("RedactedYAML: %v", err)
	}
	if strings.Contains(out, "sk-headertoken1234567890abcd") {
		t.Errorf("inline Authorization header not redacted:\n%s", out)
	}
	if !strings.Contains(out, "Bearer "+RedactedPlaceholder) {
		t.Errorf("expected the header shape to survive with the token blanked:\n%s", out)
	}
}

func TestConfig_RedactedYAML_PrunesEmptyDefaults(t *testing.T) {
	out, _, err := loadRedactFixture(t).RedactedYAML("")
	if err != nil {
		t.Fatalf("RedactedYAML: %v", err)
	}
	// Zero-value scaffolding the struct carries but the user never set.
	for _, noise := range []string{"store:", "matrix:", "upstream:", "setParams:", "stripParams:", "metadata:"} {
		if strings.Contains(out, noise) {
			t.Errorf("empty default %q should have been pruned:\n%s", noise, out)
		}
	}
	// Real values still present.
	if !strings.Contains(out, "qwen3:") || !strings.Contains(out, "upstream1:") {
		t.Errorf("pruning removed real config:\n%s", out)
	}
}

func TestConfig_RedactedYAML_PathSelectsSubtree(t *testing.T) {
	out, found, err := loadRedactFixture(t).RedactedYAML("peers.upstream1")
	if err != nil || !found {
		t.Fatalf("RedactedYAML: found=%v err=%v", found, err)
	}
	if !strings.Contains(out, "proxy: http://10.0.0.5:8080") {
		t.Errorf("subtree missing expected key:\n%s", out)
	}
	if strings.Contains(out, "models:\n    qwen3") {
		t.Errorf("subtree leaked a sibling section:\n%s", out)
	}
}

func TestConfig_RedactedYAML_UnknownPath(t *testing.T) {
	out, found, err := loadRedactFixture(t).RedactedYAML("models.does-not-exist")
	if err != nil {
		t.Fatalf("RedactedYAML: %v", err)
	}
	if found || out != "" {
		t.Errorf("found=%v out=%q, want not found", found, out)
	}
}

func TestConfig_ConfigTopLevelKeys(t *testing.T) {
	keys := loadRedactFixture(t).ConfigTopLevelKeys()
	joined := strings.Join(keys, ",")
	for _, want := range []string{"models", "peers", "macros", "apiKeys"} {
		if !strings.Contains(joined, want) {
			t.Errorf("top-level keys %v missing %q", keys, want)
		}
	}
}
