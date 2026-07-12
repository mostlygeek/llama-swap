package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
)

func unloadTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
apiKeys:
  - secret
models:
  real:
    cmd: path/to/server
    proxy: http://localhost:8080
    aliases:
      - alias
`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func newUnloadTestServer(t *testing.T) (*Server, *stubRouter) {
	t.Helper()
	local := newStubRouter([]string{"real"}, "served")
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = unloadTestConfig(t)
	s.routes() // Rebuild so auth middleware captures the configured API key.
	return s, local
}

func unloadRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/models/unload", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer secret")
	return r
}

func TestServer_OpenWebUIUnload(t *testing.T) {
	t.Run("canonical model", func(t *testing.T) {
		s, local := newUnloadTestServer(t)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, unloadRequest(`{"model":"real"}`))

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 body=%q", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		var response map[string]bool
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !response["status"] {
			t.Errorf("response = %s, want status=true", w.Body.String())
		}
		if local.unloadCalls.Load() != 1 || len(local.unloadModels) != 1 || strings.Join(local.unloadModels[0], ",") != "real" {
			t.Errorf("unloads = %#v, want [[real]]", local.unloadModels)
		}
	})

	t.Run("alias resolves to canonical model", func(t *testing.T) {
		s, local := newUnloadTestServer(t)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, unloadRequest(`{"model":"alias"}`))

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 body=%q", w.Code, w.Body.String())
		}
		if len(local.unloadModels) != 1 || strings.Join(local.unloadModels[0], ",") != "real" {
			t.Errorf("unloads = %#v, want [[real]]", local.unloadModels)
		}
	})
}

func TestServer_OpenWebUIUnload_InvalidRequest(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want int
	}{
		{name: "missing body", want: http.StatusBadRequest},
		{name: "invalid JSON", body: `{`, want: http.StatusBadRequest},
		{name: "missing model", body: `{}`, want: http.StatusBadRequest},
		{name: "non-string model", body: `{"model":1}`, want: http.StatusBadRequest},
		{name: "empty model", body: `{"model":" "}`, want: http.StatusBadRequest},
		{name: "unknown model", body: `{"model":"unknown"}`, want: http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, local := newUnloadTestServer(t)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, unloadRequest(tt.body))

			if w.Code != tt.want {
				t.Errorf("status = %d, want %d body=%q", w.Code, tt.want, w.Body.String())
			}
			if local.unloadCalls.Load() != 0 {
				t.Errorf("unload calls = %d, want 0", local.unloadCalls.Load())
			}
		})
	}
}

func TestServer_OpenWebUIUnload_Regression(t *testing.T) {
	s, local := newUnloadTestServer(t)

	t.Run("management unload all remains authenticated", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/models/unload", nil)
		r.Header.Set("Authorization", "Bearer secret")
		s.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.String() != "{\"msg\":\"ok\"}\n" {
			t.Errorf("response = %d %q, want 200 JSON success", w.Code, w.Body.String())
		}
	})

	t.Run("management named unload remains authenticated", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/models/unload/real", nil)
		r.Header.Set("Authorization", "Bearer secret")
		s.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.String() != "OK" {
			t.Errorf("response = %d %q, want 200 OK", w.Code, w.Body.String())
		}
	})

	t.Run("normal model routing remains unchanged", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"alias"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer secret")
		s.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.String() != "served" {
			t.Errorf("response = %d %q, want 200 served", w.Code, w.Body.String())
		}
	})

	if local.unloadCalls.Load() != 2 {
		t.Errorf("unload calls = %d, want 2", local.unloadCalls.Load())
	}
}
