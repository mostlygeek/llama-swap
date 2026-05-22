package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
)

func TestServer_HandleListModels(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.cfg = config.Config{
		Models: map[string]config.ModelConfig{
			"visible": {Name: "Visible", Description: "a model"},
			"hidden":  {Unlisted: true},
		},
		Peers: config.PeerDictionaryConfig{
			"peer1": {Models: []string{"remote-model"}},
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Origin", "http://example.com")
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q", got)
	}

	var resp struct {
		Data []modelRecord `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	if !ids["visible"] || !ids["remote-model"] {
		t.Errorf("missing expected models: %v", ids)
	}
	if ids["hidden"] {
		t.Error("unlisted model should not appear")
	}
}

func TestServer_HandleListModels_Aliases(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.cfg = config.Config{
		IncludeAliasesInList: true,
		Models: map[string]config.ModelConfig{
			"real": {Aliases: []string{"nick"}},
		},
	}

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	var resp struct {
		Data []modelRecord `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	ids := map[string]bool{}
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	if !ids["real"] || !ids["nick"] {
		t.Errorf("expected alias entry; got %v", ids)
	}
}

func TestServer_FindModelInPath(t *testing.T) {
	cfg := config.Config{Models: map[string]config.ModelConfig{
		"author/model": {},
		"simple":       {},
	}}

	cases := []struct {
		path      string
		wantName  string
		wantRem   string
		wantFound bool
	}{
		{"/simple/v1/chat", "simple", "/v1/chat", true},
		{"/author/model/v1/chat", "author/model", "/v1/chat", true},
		{"/author/model", "author/model", "/", true},
		{"/missing/v1", "", "", false},
		{"/", "", "", false},
	}
	for _, c := range cases {
		name, _, rem, found := findModelInPath(cfg, c.path)
		if found != c.wantFound || name != c.wantName || (found && rem != c.wantRem) {
			t.Errorf("findModelInPath(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.path, name, rem, found, c.wantName, c.wantRem, c.wantFound)
		}
	}
}

func TestServer_HandleUpstream(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "upstream-body")
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{"m1": {}}}

	t.Run("proxies to local", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/m1/v1/chat", nil))
		if w.Code != http.StatusOK || w.Body.String() != "upstream-body" {
			t.Errorf("status=%d body=%q", w.Code, w.Body.String())
		}
	})

	t.Run("redirects bare model path", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/m1", nil))
		if w.Code != http.StatusMovedPermanently {
			t.Errorf("status = %d, want 301", w.Code)
		}
	})

	t.Run("unknown model 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/nope/v1", nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

func TestServer_HandleMetrics_Unavailable(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestServer_Redirects(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	for path, want := range map[string]string{"/": "/ui", "/upstream": "/ui/models"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusFound {
			t.Errorf("%s: status = %d, want 302", path, w.Code)
		}
		if got := w.Header().Get("Location"); got != want {
			t.Errorf("%s: Location = %q, want %q", path, got, want)
		}
	}
}
