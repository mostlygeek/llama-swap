package config

import (
	"strings"
	"testing"
)

// TestConfig_SecurityCORSAbsentSelectsLegacyPolicy pins that a config
// declaring no security.cors block leaves CORS nil, which selects the
// permissive behaviour llama-swap had before the setting existed. Adding the
// setting must never change an existing deployment.
func TestConfig_SecurityCORSAbsentSelectsLegacyPolicy(t *testing.T) {
	configs := map[string]string{
		"no security block": "models:\n  m1:\n    cmd: echo ${PORT}\n",
		"empty security":    "security:\nmodels:\n  m1:\n    cmd: echo ${PORT}\n",
		"explicit wildcard is a declared block": "security:\n  cors:\n" +
			"    allowedOrigins: [\"*\"]\nmodels:\n  m1:\n    cmd: echo ${PORT}\n",
	}

	for name, yaml := range configs {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
			if err != nil {
				t.Fatalf("LoadConfigFromReader: %v", err)
			}
			if name == "explicit wildcard is a declared block" {
				// An explicit wildcard is a declared block, not an absent one.
				if cfg.Security.CORS == nil {
					t.Fatal("CORS is nil for a declared block")
				}
				return
			}
			if cfg.Security.CORS != nil {
				t.Errorf("CORS = %+v, want nil so the legacy policy applies", cfg.Security.CORS)
			}
		})
	}
}

// TestConfig_SecurityCORSDeclaredRequiresOrigins covers the second half of the
// two-mode model: declaring the block means taking charge of access, so a
// block that names no origins is a half-finished edit and fails at startup
// rather than silently blocking every browser.
func TestConfig_SecurityCORSDeclaredRequiresOrigins(t *testing.T) {
	configs := map[string]string{
		"empty cors mapping": "security:\n  cors: {}\nmodels:\n  m1:\n    cmd: echo ${PORT}\n",
		"cors with null value": "security:\n  cors:\nmodels:\n  m1:\n" +
			"    cmd: echo ${PORT}\n",
		"empty allowedOrigins": "security:\n  cors:\n    allowedOrigins: []\n" +
			"models:\n  m1:\n    cmd: echo ${PORT}\n",
		"only maxAge set": "security:\n  cors:\n    maxAge: 600\n" +
			"models:\n  m1:\n    cmd: echo ${PORT}\n",
	}

	for name, yaml := range configs {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(yaml))
			if err == nil {
				t.Fatal("LoadConfigFromReader accepted a security.cors block with no origins")
			}
			if !strings.Contains(err.Error(), "allowedOrigins") {
				t.Errorf("error = %q, want it to name allowedOrigins", err)
			}
		})
	}
}

// TestConfig_SecurityCORSDeclaredKeepsPreflightDefaults checks the other side
// of the model: only allowedOrigins loses its default. The preflight mechanics
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
	if cors == nil {
		t.Fatal("CORS is nil for a declared block")
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
	if len(cors.AllowedMethods) != 2 {
		t.Errorf("allowedMethods=%q", cors.AllowedMethods)
	}
	if cors.MaxAge != 600 {
		t.Errorf("maxAge=%d want 600", cors.MaxAge)
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
			name:    "no origins at all",
			cors:    CORSConfig{},
			wantErr: "allowedOrigins",
		},
		{
			name:    "origins set but empty",
			cors:    CORSConfig{AllowedOrigins: []string{}, MaxAge: 600},
			wantErr: "allowedOrigins",
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
