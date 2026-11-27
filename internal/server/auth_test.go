package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
)

func TestServer_RequestContextMiddleware(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"llama3": {},
		},
	}

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := CreateRequestContextMiddleware(cfg)

	t.Run("known model passes through", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"llama3"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mw(final).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("missing model returns 404", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mw(final).ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

func TestServer_AuthMiddleware(t *testing.T) {
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// TestServer_CrossSiteBasicAuthRejected reproduces GHSA-g4gp-qf3f-4x4w: once a
// browser holds Basic credentials from the login prompt it attaches them to
// requests other sites start (an <img>, a form post). Those must not unload
// models or start work, while the UI's own same-origin requests and API
// clients keep working.
func TestServer_CrossSiteBasicAuthRejected(t *testing.T) {
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:secret"))

	newServer := func() (*Server, *stubRouter) {
		local := newStubRouter([]string{"m1"}, "")
		s := newTestServerWithConfig(config.Config{RequiredAPIKeys: []string{"secret"}}, local, newStubRouter(nil, ""))
		return s, local
	}

	cases := []struct {
		name, method, path, auth, site, origin string
		wantCode                               int
		wantUnload                             int32
	}{
		{"img to /unload", http.MethodGet, "/unload", basic, "cross-site", "", http.StatusForbidden, 0},
		{"form post unload", http.MethodPost, "/api/models/unload", basic, "cross-site", "https://evil.example", http.StatusForbidden, 0},
		{"same-site post unload", http.MethodPost, "/api/models/unload", basic, "same-site", "http://localhost:3000", http.StatusForbidden, 0},
		{"old browser foreign origin", http.MethodPost, "/api/models/unload", basic, "", "https://evil.example", http.StatusForbidden, 0},
		{"ui same-origin", http.MethodPost, "/api/models/unload", basic, "same-origin", "http://example.com", http.StatusOK, 1},
		{"typed url", http.MethodGet, "/unload", basic, "none", "", http.StatusOK, 1},
		{"non-browser basic", http.MethodPost, "/api/models/unload", basic, "", "", http.StatusOK, 1},
		{"bearer cross-site", http.MethodPost, "/api/models/unload", "Bearer secret", "cross-site", "https://evil.example", http.StatusOK, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, local := newServer()
			r := httptest.NewRequest(c.method, c.path, nil)
			r.Header.Set("Authorization", c.auth)
			if c.site != "" {
				r.Header.Set("Sec-Fetch-Site", c.site)
			}
			if c.origin != "" {
				r.Header.Set("Origin", c.origin)
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != c.wantCode {
				t.Errorf("status = %d, want %d", w.Code, c.wantCode)
			}
			if got := local.unloadCalls.Load(); got != c.wantUnload {
				t.Errorf("unloadCalls = %d, want %d", got, c.wantUnload)
			}
		})
	}
}

func TestServer_IsCrossSiteBasicAuth(t *testing.T) {
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:secret"))
	lowerBasic := "basic " + base64.StdEncoding.EncodeToString([]byte("user:secret"))
	cases := []struct {
		name, method, path, auth, site, mode, origin string
		want                                         bool
	}{
		{"x-api-key only", "POST", "/api/models/unload", "", "cross-site", "no-cors", "https://evil.example", false},
		{"lowercase basic scheme cross-site", "GET", "/unload", lowerBasic, "cross-site", "no-cors", "", true},
		{"cross-site json as text/plain", "POST", "/v1/chat/completions", basic, "cross-site", "navigate", "https://evil.example", true},
		{"cross-site link to ui", "GET", "/ui/", basic, "cross-site", "navigate", "", false},
		{"cross-site fetch of ui", "GET", "/ui/", basic, "cross-site", "no-cors", "", true},
		{"cross-site navigate to unload", "GET", "/unload", basic, "cross-site", "navigate", "", true},
		{"old browser same origin", "POST", "/api/models/pause", basic, "", "", "http://localhost:8080", false},
		{"old browser null origin", "POST", "/api/models/pause", basic, "", "", "null", true},
		{"old browser cross-site get", "GET", "/running", basic, "", "", "https://evil.example", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://localhost:8080"+c.path, nil)
		if c.auth != "" {
			r.Header.Set("Authorization", c.auth)
		}
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		if c.mode != "" {
			r.Header.Set("Sec-Fetch-Mode", c.mode)
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := isCrossSiteBasicAuth(r); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
