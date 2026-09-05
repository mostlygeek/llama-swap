package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
)

func newTailcatPolicyServer(t *testing.T, extra string) *Server {
	t.Helper()
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
models:
  real:
    proxy: http://localhost:1
    aliases: [public]
  hidden:
    proxy: http://localhost:2
tailcat:
  models: [public]
` + extra))
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetTailcatEnabled(true)
	s := newTestServer(newStubRouter([]string{"real", "hidden"}, "ok"), newStubRouter(nil, ""))
	s.cfg = cfg
	s.routes()
	return s
}

func tailcatRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func TestServer_TailcatNonAdminStrictSurface(t *testing.T) {
	s := newTailcatPolicyServer(t, "")
	tests := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/health", http.StatusOK},
		{http.MethodGet, "/api/version", http.StatusNotFound},
		{http.MethodGet, "/ui/", http.StatusNotFound},
		{http.MethodGet, "/logs", http.StatusNotFound},
		{http.MethodGet, "/metrics", http.StatusNotFound},
		{http.MethodGet, "/running", http.StatusNotFound},
		{http.MethodGet, "/upstream/real", http.StatusNotFound},
		{http.MethodGet, "/", http.StatusNotFound},
		{http.MethodOptions, "/v1/chat/completions", http.StatusNoContent},
		{http.MethodOptions, "/api/version", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.ServeTailcatHTTP(w, tailcatRequest(tt.method, tt.path, ""))
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d, body=%q", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestServer_TailcatAllowlistUsesOriginalPublicID(t *testing.T) {
	s := newTailcatPolicyServer(t, "")

	w := httptest.NewRecorder()
	s.ServeTailcatHTTP(w, tailcatRequest(http.MethodPost, "/v1/chat/completions", `{"model":"public"}`))
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("public alias response = %d %q", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	s.ServeTailcatHTTP(w, tailcatRequest(http.MethodPost, "/v1/chat/completions", `{"model":"real"}`))
	if w.Code != http.StatusNotFound {
		t.Fatalf("real ID status = %d, want 404", w.Code)
	}
}

func TestServer_TailcatAPIKeyComposesWithNodeAuthorization(t *testing.T) {
	s := newTailcatPolicyServer(t, "\napiKeys: [secret]\n")
	r := tailcatRequest(http.MethodPost, "/v1/chat/completions", `{"model":"public"}`)
	w := httptest.NewRecorder()
	s.ServeTailcatHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing key status = %d", w.Code)
	}

	r = tailcatRequest(http.MethodPost, "/v1/chat/completions", `{"model":"public"}`)
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	s.ServeTailcatHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("valid key status = %d body=%q", w.Code, w.Body.String())
	}
}

func TestServer_TailcatFiltersModelListing(t *testing.T) {
	s := newTailcatPolicyServer(t, "\nincludeAliasesInList: true\n")
	w := httptest.NewRecorder()
	s.ServeTailcatHTTP(w, tailcatRequest(http.MethodGet, "/v1/models", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].ID != "public" {
		t.Fatalf("listed models = %+v", response.Data)
	}
}

func TestServer_TailcatAdminUnlocksNormalSurface(t *testing.T) {
	s := newTailcatPolicyServer(t, "")
	s.cfg.Tailcat.Admin = true
	w := httptest.NewRecorder()
	s.ServeTailcatHTTP(w, tailcatRequest(http.MethodGet, "/api/version", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("admin API status = %d body=%q", w.Code, w.Body.String())
	}
}

func TestServer_TailcatExposedModelIDsActiveProfileAndBarePeerNames(t *testing.T) {
	s := newTailcatPolicyServer(t, `
peers:
  gpu:
    proxy: http://localhost:3
    models: [chat]
profiles:
  dev:
    pins:
      dev-only: real
  prod:
    pins:
      prod-only: real
`)
	s.cfg.Tailcat.Models = []string{"*"}

	if got := s.tailcatExposedModelIDs(); contains(got, "dev-only") || contains(got, "prod-only") {
		t.Fatalf("no active profile: got %v, want neither profile's pins listed", got)
	}

	if _, err := s.setActiveProfile("prod"); err != nil {
		t.Fatal(err)
	}
	got := s.tailcatExposedModelIDs()
	if !contains(got, "prod-only") {
		t.Fatalf("active profile's pin missing: %v", got)
	}
	if contains(got, "dev-only") {
		t.Fatalf("inactive profile's pin leaked: %v", got)
	}
	if !contains(got, "gpu/chat") {
		t.Fatalf("peer fully qualified name missing: %v", got)
	}
	if !contains(got, "chat") {
		t.Fatalf("unambiguous bare peer model name missing: %v", got)
	}
}

func TestServer_APITailcatStatus(t *testing.T) {
	s := newTailcatPolicyServer(t, "")
	s.SetTailcatAddress("tcCaseSensitiveToken")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, tailcatRequest(http.MethodGet, "/api/tailcat", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":true`) ||
		!strings.Contains(w.Body.String(), "tcCaseSensitiveToken") ||
		!strings.Contains(w.Body.String(), `"models":["public"]`) {
		t.Fatalf("status response = %d %q", w.Code, w.Body.String())
	}

	s.cfg.SetTailcatEnabled(false)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, tailcatRequest(http.MethodGet, "/api/tailcat", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) ||
		!strings.Contains(w.Body.String(), `"models":[]`) {
		t.Fatalf("disabled status response = %d %q, want models: []", w.Code, w.Body.String())
	}
}
