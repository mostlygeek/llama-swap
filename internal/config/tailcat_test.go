package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

func testTailcatBlob() string {
	private := tailcat.NewPrivateKey()
	private.Public.RegionID = 1
	return string(private.Public.ConnBlob())
}

func writeTailcatKey(t *testing.T) string {
	t.Helper()
	private := tailcat.NewPrivateKey()
	data, err := json.Marshal(private)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tailcat.private.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfig_TailcatValidation(t *testing.T) {
	client := key.NewNode().Public().String()
	yaml := fmt.Sprintf(`
models:
  local:
    proxy: http://localhost:1
    aliases: [alias]
selectors:
  select:
    strategy: pin
    targets: [local]
profiles:
  prod:
    pins:
      public: local
tailcat:
  allow: [%q, %q]
  models: [local, alias, select, public, "*", local]
  debug: true
`, client, client)
	cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	if cfg.Tailcat == nil {
		t.Fatal("Tailcat config was not retained")
	}
	if !cfg.Tailcat.Debug {
		t.Fatal("Debug = false, want true")
	}
	if got := len(cfg.Tailcat.AllowedClients); got != 1 {
		t.Fatalf("AllowedClients = %d, want 1", got)
	}
	wantModels := []string{"local", "alias", "select", "public", "*"}
	if fmt.Sprint(cfg.Tailcat.Models) != fmt.Sprint(wantModels) {
		t.Fatalf("Models = %v, want %v", cfg.Tailcat.Models, wantModels)
	}
}

func TestConfig_ExposedTailcatModels(t *testing.T) {
	yaml := `
models:
  local:
    proxy: http://localhost:1
    aliases: [alias]
selectors:
  select:
    strategy: pin
    targets: [local]
profiles:
  prod:
    pins:
      public: local
tailcat:
  models: ["*"]
`
	cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	want := []string{"alias", "local", "public", "select"}
	if got := ExposedTailcatModels(cfg); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ExposedTailcatModels = %v, want %v", got, want)
	}

	cfg.Tailcat.Models = []string{"local", "alias"}
	want = []string{"alias", "local"}
	if got := ExposedTailcatModels(cfg); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ExposedTailcatModels = %v, want %v", got, want)
	}

	cfg.Tailcat = nil
	if got := ExposedTailcatModels(cfg); got != nil {
		t.Fatalf("ExposedTailcatModels = %v, want nil", got)
	}
}

func TestConfig_TailcatRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"bad allow", "tailcat:\n  allow: [abc]\nmodels: {}\n", "tailcat.allow[0]: invalid node public key"},
		{"unknown model with wildcard", "tailcat:\n  models: ['*', missing]\nmodels: {}\n", "tailcat.models[1]: unknown public model ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestConfig_TailcatPeerURLValidation(t *testing.T) {
	blob := testTailcatBlob()
	valid := fmt.Sprintf("models: {}\npeers:\n  cat:\n    proxy: tailcat://%s\n    models: [m]\n", blob)
	cfg, err := LoadConfigFromReader(strings.NewReader(valid))
	if err != nil {
		t.Fatalf("valid Tailcat peer: %v", err)
	}
	key, gotBlob, privateKey, found := cfg.Peers["cat"].Tailcat()
	if !found || key != "" || gotBlob != blob || privateKey != nil || cfg.Peers["cat"].ProxyURL.Scheme != "http" {
		t.Fatalf("peer was not normalized: %+v", cfg.Peers["cat"])
	}

	bad := []string{
		"tailcat://user@" + blob,
		"tailcat://" + blob + "/path",
		"tailcat://" + blob + "?x=1",
		"tailcat://" + blob + "#frag",
		"tailcat://" + blob + ":80",
		"tailcat://not-a-token",
	}
	for _, proxy := range bad {
		t.Run(proxy, func(t *testing.T) {
			yaml := fmt.Sprintf("models: {}\npeers:\n  cat:\n    proxy: %q\n    models: [m]\n", proxy)
			if _, err := LoadConfigFromReader(strings.NewReader(yaml)); err == nil {
				t.Fatalf("accepted malformed Tailcat URL %q", proxy)
			}
		})
	}
}

func TestConfig_PeerTailcatAccessor(t *testing.T) {
	blob := testTailcatBlob()
	keyPath := writeTailcatKey(t)
	yaml := fmt.Sprintf("models: {}\npeers:\n  cat:\n    proxy: tailcat://%s\n    tailcatKey: %q\n    models: [m]\n", blob, keyPath)
	cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}

	key, gotBlob, privateKey, found := cfg.Peers["cat"].Tailcat()
	if !found || key != keyPath || gotBlob != blob || privateKey == nil {
		t.Fatalf("Tailcat() = (%q, %q, %v, %t)", key, gotBlob, privateKey, found)
	}
	if _, _, _, found := (PeerConfig{Proxy: "http://localhost"}).Tailcat(); found {
		t.Fatal("non-Tailcat peer reported Tailcat settings")
	}
}

func TestConfig_TailcatPeerURLUppercaseScheme(t *testing.T) {
	blob := testTailcatBlob()
	keyPath := writeTailcatKey(t)
	yaml := fmt.Sprintf("models: {}\npeers:\n  cat:\n    proxy: TAILCAT://%s\n    tailcatKey: %q\n    models: [m]\n", blob, keyPath)
	cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}

	key, gotBlob, privateKey, found := cfg.Peers["cat"].Tailcat()
	if !found || key != keyPath || gotBlob != blob || privateKey == nil || cfg.Peers["cat"].ProxyURL.Scheme != "http" {
		t.Fatalf("peer was not normalized: %+v", cfg.Peers["cat"])
	}
}

func TestConfig_TailcatKeyOnlyForTailcatPeer(t *testing.T) {
	yaml := "models: {}\npeers:\n  p:\n    proxy: http://localhost\n    tailcatKey: ephemeral\n    models: [m]\n"
	_, err := LoadConfigFromReader(strings.NewReader(yaml))
	if err == nil || !strings.Contains(err.Error(), "only valid with a tailcat://") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfig_TailcatConnectionTokenIsRedacted(t *testing.T) {
	blob := testTailcatBlob()
	yaml := fmt.Sprintf("models: {}\npeers:\n  cat:\n    proxy: tailcat://%s\n    models: [m]\n", blob)
	cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := cfg.RedactedYAML("peers.cat")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, blob) || !strings.Contains(out, RedactedPlaceholder) {
		t.Fatalf("redacted config leaked Tailcat token: %s", out)
	}
}
