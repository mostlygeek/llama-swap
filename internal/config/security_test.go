package config

import (
	"strings"
	"testing"
)

// TestConfig_SecurityCORSDefaultsWhenAbsent pins that a config with no
// security block keeps the permissive behaviour llama-swap had before the
// setting existed, so adding it never changes an existing deployment.
func TestConfig_SecurityCORSDefaultsWhenAbsent(t *testing.T) {
	configs := map[string]string{
		"no security block":  "models:\n  m1:\n    cmd: echo ${PORT}\n",
		"empty security":     "security:\nmodels:\n  m1:\n    cmd: echo ${PORT}\n",
		"empty cors":         "security:\n  cors: {}\nmodels:\n  m1:\n    cmd: echo ${PORT}\n",
		"empty allowOrigins": "security:\n  cors:\n    allowedOrigins: []\nmodels:\n  m1:\n    cmd: echo ${PORT}\n",
	}

	for name, yaml := range configs {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadConfigFromReader(strings.NewReader(yaml))
			if err != nil {
				t.Fatalf("LoadConfigFromReader: %v", err)
			}
			cors := cfg.Security.CORS
			if cors.AllowCredentials {
				t.Error("allowCredentials defaulted to true")
			}
			if cors.MaxAge != 0 {
				t.Errorf("maxAge=%d want 0, which newCORSPolicy resolves to the default", cors.MaxAge)
			}
			if err := cors.Validate(); err != nil {
				t.Errorf("default config failed validation: %v", err)
			}
		})
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
			name:    "negative maxAge",
			cors:    CORSConfig{MaxAge: -1},
			wantErr: "maxAge",
		},
		{
			name:    "non-token method",
			cors:    CORSConfig{AllowedMethods: []string{"GET POST"}},
			wantErr: "allowedMethods",
		},
		{
			name:    "non-token allowed header",
			cors:    CORSConfig{AllowedHeaders: []string{"Bad Header"}},
			wantErr: "allowedHeaders",
		},
		{
			name:    "non-token exposed header",
			cors:    CORSConfig{ExposedHeaders: []string{"Bad@Header"}},
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
