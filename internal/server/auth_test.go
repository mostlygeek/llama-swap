package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
)

func TestServer_ExtractAPIKey(t *testing.T) {
	basicHeader := func(user, pass string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	cases := []struct {
		name string
		auth string
		xapi string
		want string
	}{
		{"none", "", "", ""},
		{"bearer", "Bearer tok123", "", "tok123"},
		{"basic", basicHeader("user", "pw-key"), "", "pw-key"},
		{"x-api-key", "", "xkey", "xkey"},
		{"basic beats bearer", basicHeader("u", "bk"), "", "bk"},
		{"bearer beats x-api-key", "Bearer btok", "xkey", "btok"},
		{"malformed basic falls back to x-api-key", "Basic !!!notbase64", "xkey", "xkey"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.auth != "" {
				r.Header.Set("Authorization", c.auth)
			}
			if c.xapi != "" {
				r.Header.Set("x-api-key", c.xapi)
			}
			if got := extractAPIKey(r); got != c.want {
				t.Errorf("extractAPIKey() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestServer_SanitizeAccessControlRequestHeaders(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Content-Type, Authorization", "Content-Type, Authorization"},
		{"  X-Custom ,  Accept ", "X-Custom, Accept"},
		{"Valid, Bad Header", "Valid"},
		{"Bad@Header", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeAccessControlRequestHeaderValues(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestServer_IsTokenChar(t *testing.T) {
	for _, r := range "abcXYZ0129!#$%&'*+-.^_`|~" {
		if !isTokenChar(r) {
			t.Errorf("isTokenChar(%q) = false, want true", r)
		}
	}
	for _, r := range " @()/\t\"" {
		if isTokenChar(r) {
			t.Errorf("isTokenChar(%q) = true, want false", r)
		}
	}
}

func TestServer_AuthMiddleware(t *testing.T) {
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
			t.Error("auth headers leaked to upstream")
		}
		w.WriteHeader(http.StatusOK)
	})

	t.Run("no keys configured passes through", func(t *testing.T) {
		mw := CreateAuthMiddleware(config.Config{})
		w := httptest.NewRecorder()
		mw(final).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	cfg := config.Config{RequiredAPIKeys: []string{"secret"}}

	t.Run("valid key", func(t *testing.T) {
		mw := CreateAuthMiddleware(cfg)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		mw(final).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("invalid key", func(t *testing.T) {
		mw := CreateAuthMiddleware(cfg)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer wrong")
		w := httptest.NewRecorder()
		mw(final).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
		if w.Header().Get("WWW-Authenticate") == "" {
			t.Error("missing WWW-Authenticate header")
		}
	})
}
