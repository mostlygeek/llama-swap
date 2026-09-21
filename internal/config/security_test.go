package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestConfig_SecurityCORSUnsetOriginsSelectLegacyPolicy pins that a config
// naming no origins keeps the permissive behaviour llama-swap had before the
// setting existed. Adding the setting must never change an existing
// deployment, and every way of writing "nothing configured" means the same.
func TestConfig_SecurityCORSUnsetOriginsSelectLegacyPolicy(t *testing.T) {
	const models = "models:\n  m1:\n    cmd: echo ${PORT}\n"
	configs := map[string]string{
		"no security block":            models,
		"empty security":               "security:\n" + models,
		"cors with null value":         "security:\n  cors:\n" + models,
		"empty cors mapping":           "security:\n  cors: {}\n" + models,
		"allowedOrigins with no value": "security:\n  cors:\n    allowedOrigins:\n" + models,
	}

	for name, yaml := range configs {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
			if err != nil {
				t.Fatalf("LoadConfigFromReader: %v", err)
			}
			if got := cfg.Security.CORS.AllowedOrigins; len(got) != 0 {
				t.Errorf("allowedOrigins = %q, want empty so the legacy policy applies", got)
			}
		})
	}
}

// TestConfig_SecurityCORSEmptyOriginListRejected covers a list written as
// empty. Reading `allowedOrigins: []` as "allow every origin" would invert
// what it plainly says, so it fails at startup instead.
func TestConfig_SecurityCORSEmptyOriginListRejected(t *testing.T) {
	const models = "models:\n  m1:\n    cmd: echo ${PORT}\n"
	configs := map[string]string{
		"flow empty list":           "security:\n  cors:\n    allowedOrigins: []\n" + models,
		"flow empty list with more": "security:\n  cors:\n    allowedOrigins: []\n    maxAge: 600\n" + models,
	}

	for name, yaml := range configs {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(yaml))
			if err == nil {
				t.Fatal("LoadConfigFromReader accepted an empty allowedOrigins list")
			}
			if !strings.Contains(err.Error(), "at least one origin") {
				t.Errorf("error = %q, want it to ask for at least one origin", err)
			}
		})
	}
}

// TestConfig_SecurityCORSNilVsEmptyOrigins pins the yaml.v3 behaviour that
// telling an absent allowedOrigins from an empty one depends on: an absent key
// leaves the slice nil, while `allowedOrigins: []` produces a non-nil empty
// one. If a yaml.v3 upgrade ever collapsed the two, the empty-list rule above
// would quietly become a no-op, so fail here instead.
func TestConfig_SecurityCORSNilVsEmptyOrigins(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantNil bool
	}{
		{"absent key", "maxAge: 600\n", true},
		{"null value", "allowedOrigins:\n", true},
		{"explicit null", "allowedOrigins: null\n", true},
		{"flow empty list", "allowedOrigins: []\n", false},
		{"block empty list", "allowedOrigins: [\n]\n", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var cors CORSConfig
			if err := yaml.Unmarshal([]byte(c.yaml), &cors); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got := cors.AllowedOrigins == nil; got != c.wantNil {
				t.Errorf("AllowedOrigins == nil is %v, want %v", got, c.wantNil)
			}
			if len(cors.AllowedOrigins) != 0 {
				t.Errorf("AllowedOrigins = %q, want empty", cors.AllowedOrigins)
			}
		})
	}
}

// TestConfig_SecurityCORSOrphanSettingsRejected covers the other half of the
// two-mode model. A config that tunes CORS but never says which origins the
// tuning applies to is a half-finished edit; falling back to allow-all there
// would widen access for a config that meant to restrict it.
func TestConfig_SecurityCORSOrphanSettingsRejected(t *testing.T) {
	const models = "models:\n  m1:\n    cmd: echo ${PORT}\n"
	configs := map[string]string{
		"allowCredentials alone":    "security:\n  cors:\n    allowCredentials: true\n" + models,
		"allowPrivateNetwork alone": "security:\n  cors:\n    allowPrivateNetwork: true\n" + models,
		"allowedMethods alone":      "security:\n  cors:\n    allowedMethods: [\"GET\"]\n" + models,
		"allowedHeaders alone":      "security:\n  cors:\n    allowedHeaders: [\"Content-Type\"]\n" + models,
		"exposedHeaders alone":      "security:\n  cors:\n    exposedHeaders: [\"X-Request-Id\"]\n" + models,
		"maxAge alone":              "security:\n  cors:\n    maxAge: 600\n" + models,
		"several without origins": "security:\n  cors:\n    maxAge: 600\n" +
			"    allowedMethods: [\"GET\"]\n" + models,
	}

	for name, yaml := range configs {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(yaml))
			if err == nil {
				t.Fatal("LoadConfigFromReader accepted cors settings with no allowedOrigins")
			}
			if !strings.Contains(err.Error(), "allowedOrigins") {
				t.Errorf("error = %q, want it to name allowedOrigins", err)
			}
			if !strings.Contains(err.Error(), "security.cors") {
				t.Errorf("error = %q, want it prefixed with security.cors", err)
			}
		})
	}
}

// TestConfig_SecurityCORSDeclaredKeepsPreflightDefaults checks that naming
// origins does not force you to spell out the rest. The preflight mechanics
// stay empty in the config and are resolved to defaults by newCORSPolicy, so a
// block that only names origins still works in a browser.
func TestConfig_SecurityCORSDeclaredKeepsPreflightDefaults(t *testing.T) {
	const yaml = `
security:
  cors:
    allowedOrigins: ["https://dash.example.com"]
models:
  m1:
    cmd: echo ${PORT}
`
	cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	cors := cfg.Security.CORS
	if len(cors.AllowedOrigins) != 1 {
		t.Fatalf("allowedOrigins = %q, want the one configured origin", cors.AllowedOrigins)
	}
	if len(cors.AllowedMethods) != 0 || len(cors.AllowedHeaders) != 0 || cors.MaxAge != 0 {
		t.Errorf("unset preflight fields were populated at load: %+v", cors)
	}
}

func TestConfig_SecurityCORSParsing(t *testing.T) {
	const yaml = `
security:
  cors:
    allowedOrigins:
      - "https://dash.example.com"
      - "http://localhost:5173"
    allowCredentials: true
    allowPrivateNetwork: true
    allowedMethods: ["GET", "POST"]
    allowedHeaders: ["Content-Type"]
    exposedHeaders: ["X-Request-Id"]
    maxAge: 600
models:
  m1:
    cmd: echo ${PORT}
`
	cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}

	cors := cfg.Security.CORS
	if len(cors.AllowedOrigins) != 2 || cors.AllowedOrigins[0] != "https://dash.example.com" {
		t.Errorf("allowedOrigins=%q", cors.AllowedOrigins)
	}
	if !cors.AllowCredentials {
		t.Error("allowCredentials=false want true")
	}
	if !cors.AllowPrivateNetwork {
		t.Error("allowPrivateNetwork=false want true")
	}
	if len(cors.AllowedMethods) != 2 {
		t.Errorf("allowedMethods=%q", cors.AllowedMethods)
	}
	if cors.MaxAge != 600 {
		t.Errorf("maxAge=%d want 600", cors.MaxAge)
	}
}

// TestConfig_SecurityCORSPrivateNetworkRequiresExplicitOrigins covers the two
// ways of asking for private-network access without naming who gets it.
// Granting it to every origin would let any page in any open tab drive a
// llama-swap on the user's own network.
func TestConfig_SecurityCORSPrivateNetworkRequiresExplicitOrigins(t *testing.T) {
	const models = "models:\n  m1:\n    cmd: echo ${PORT}\n"
	cases := map[string]struct {
		yaml    string
		wantErr string
	}{
		"with a wildcard origin": {
			yaml: "security:\n  cors:\n    allowedOrigins: [\"*\"]\n" +
				"    allowPrivateNetwork: true\n" + models,
			wantErr: "allowPrivateNetwork",
		},
		"with no origins at all": {
			yaml:    "security:\n  cors:\n    allowPrivateNetwork: true\n" + models,
			wantErr: "allowedOrigins is required",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(c.yaml))
			if err == nil {
				t.Fatal("LoadConfigFromReader accepted allowPrivateNetwork without explicit origins")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %q, want it to name %q", err, c.wantErr)
			}
		})
	}
}

func TestConfig_SecurityCORSValidation(t *testing.T) {
	cases := []struct {
		name    string
		cors    CORSConfig
		wantErr string // substring the message must name
	}{
		{
			name:    "wildcard with credentials",
			cors:    CORSConfig{AllowedOrigins: []string{"*"}, AllowCredentials: true},
			wantErr: "allowCredentials",
		},
		{
			name:    "wildcard with private network",
			cors:    CORSConfig{AllowedOrigins: []string{"*"}, AllowPrivateNetwork: true},
			wantErr: "allowPrivateNetwork",
		},
		{
			name:    "wildcard among explicit origins with credentials",
			cors:    CORSConfig{AllowedOrigins: []string{"https://a.example.com", "*"}, AllowCredentials: true},
			wantErr: "allowCredentials",
		},
		{
			name:    "origin with trailing slash",
			cors:    CORSConfig{AllowedOrigins: []string{"https://dash.example.com/"}},
			wantErr: "path",
		},
		{
			name:    "origin with path",
			cors:    CORSConfig{AllowedOrigins: []string{"https://dash.example.com/ui"}},
			wantErr: "path",
		},
		{
			name:    "origin without scheme",
			cors:    CORSConfig{AllowedOrigins: []string{"dash.example.com"}},
			wantErr: "scheme://host",
		},
		{
			name:    "empty origin",
			cors:    CORSConfig{AllowedOrigins: []string{""}},
			wantErr: "must not be empty",
		},
		{
			name:    "origin with whitespace",
			cors:    CORSConfig{AllowedOrigins: []string{" https://dash.example.com"}},
			wantErr: "whitespace",
		},
		{
			name:    "explicitly empty origin list",
			cors:    CORSConfig{AllowedOrigins: []string{}},
			wantErr: "at least one origin",
		},
		{
			name:    "maxAge without origins",
			cors:    CORSConfig{MaxAge: 600},
			wantErr: "allowedOrigins is required",
		},
		{
			name:    "credentials without origins",
			cors:    CORSConfig{AllowCredentials: true},
			wantErr: "allowedOrigins is required",
		},
		{
			name:    "empty origin list beats the orphan rule",
			cors:    CORSConfig{AllowedOrigins: []string{}, MaxAge: 600},
			wantErr: "at least one origin",
		},
		{
			name:    "negative maxAge",
			cors:    CORSConfig{AllowedOrigins: []string{"*"}, MaxAge: -1},
			wantErr: "maxAge",
		},
		{
			name:    "non-token method",
			cors:    CORSConfig{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET POST"}},
			wantErr: "allowedMethods",
		},
		{
			name:    "non-token allowed header",
			cors:    CORSConfig{AllowedOrigins: []string{"*"}, AllowedHeaders: []string{"Bad Header"}},
			wantErr: "allowedHeaders",
		},
		{
			name:    "non-token exposed header",
			cors:    CORSConfig{AllowedOrigins: []string{"*"}, ExposedHeaders: []string{"Bad@Header"}},
			wantErr: "exposedHeaders",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cors.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error naming %q", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("Validate() = %q, want it to name %q", err, c.wantErr)
			}
		})
	}
}

// TestConfig_SecurityCORSValidationRunsOnLoad checks the validation is actually
// wired into LoadConfigFromReader, not just reachable on the type.
func TestConfig_SecurityCORSValidationRunsOnLoad(t *testing.T) {
	const yaml = `
security:
  cors:
    allowedOrigins: ["*"]
    allowCredentials: true
models:
  m1:
    cmd: echo ${PORT}
`
	_, err := LoadConfigFromReader(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("LoadConfigFromReader accepted \"*\" with allowCredentials")
	}
	if !strings.Contains(err.Error(), "security.cors") {
		t.Errorf("error = %q, want it prefixed with security.cors", err)
	}
}

func TestConfig_SecurityCORSValidOrigins(t *testing.T) {
	valid := []string{
		"*",
		"https://dash.example.com",
		"http://localhost:5173",
		"http://127.0.0.1:8080",
		"https://sub.domain.example.com:8443",
	}
	for _, origin := range valid {
		cors := CORSConfig{AllowedOrigins: []string{origin}}
		if err := cors.Validate(); err != nil {
			t.Errorf("Validate() rejected %q: %v", origin, err)
		}
	}
}

func TestConfig_IsHTTPToken(t *testing.T) {
	for _, s := range []string{"abcXYZ0129", "Content-Type", "X-Custom", "!#$%&'*+-.^_`|~"} {
		if !IsHTTPToken(s) {
			t.Errorf("IsHTTPToken(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", " ", "Bad Header", "Bad@Header", "a/b", "quote\"d", "tab\there"} {
		if IsHTTPToken(s) {
			t.Errorf("IsHTTPToken(%q) = true, want false", s)
		}
	}
}
