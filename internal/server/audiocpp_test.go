package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

func TestServer_HandleAudioCpp(t *testing.T) {
	local := newStubRouter([]string{config.AudioCppModelID}, "")
	var gotPath, gotQuery string
	var gotContext swaputil.ReqContextData
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		gotContext, _ = swaputil.ReadContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{config.AudioCppModelID: {}}}
	s.routes()

	t.Run("redirects bare path", func(t *testing.T) {
		for _, tt := range []struct {
			method string
			status int
		}{
			{http.MethodGet, http.StatusMovedPermanently},
			{http.MethodHead, http.StatusMovedPermanently},
			{http.MethodPost, http.StatusPermanentRedirect},
		} {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(tt.method, "/audiocpp?token=value", nil))
			if w.Code != tt.status {
				t.Errorf("%s status=%d want %d", tt.method, w.Code, tt.status)
			}
			if got := w.Header().Get("Location"); got != "audiocpp/?token=value" {
				t.Errorf("%s Location=%q want audiocpp/?token=value", tt.method, got)
			}
		}
	})

	t.Run("strips prefix and binds the fixed model", func(t *testing.T) {
		for _, tt := range []struct {
			method    string
			target    string
			wantPath  string
			wantQuery string
		}{
			{http.MethodGet, "/audiocpp/?token=value", "/", "token=value"},
			{http.MethodGet, "/audiocpp/assets/app.js", "/assets/app.js", ""},
			{http.MethodPost, "/audiocpp/v1/tasks/run", "/v1/tasks/run", ""},
			{http.MethodGet, "/audiocpp/api/models%2Fa.gguf", "/api/models%2Fa.gguf", ""},
		} {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(tt.method, tt.target, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("%s status=%d want 200", tt.target, w.Code)
			}
			if gotPath != tt.wantPath || gotQuery != tt.wantQuery {
				t.Errorf("%s path=%q query=%q want path=%q query=%q",
					tt.target, gotPath, gotQuery, tt.wantPath, tt.wantQuery)
			}
			if gotContext.Model != config.AudioCppModelID || gotContext.ModelID != config.AudioCppModelID {
				t.Errorf("%s context=%+v want model %s", tt.target, gotContext, config.AudioCppModelID)
			}
		}
	})
}

func TestServer_HandleAudioCpp_RequiresExactLocalModel(t *testing.T) {
	for _, tt := range []struct {
		name  string
		local *stubRouter
		peer  *stubRouter
		cfg   map[string]config.ModelConfig
	}{
		{"not configured", newStubRouter(nil, ""), newStubRouter(nil, ""), nil},
		{"only on a peer", newStubRouter(nil, ""), newStubRouter([]string{config.AudioCppModelID}, "peer"), nil},
		{"configured but not local", newStubRouter(nil, ""), newStubRouter(nil, ""),
			map[string]config.ModelConfig{config.AudioCppModelID: {}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(tt.local, tt.peer)
			s.cfg = config.Config{Models: tt.cfg}
			s.routes()
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/audiocpp/", nil))
			if w.Code != http.StatusNotFound {
				t.Errorf("status=%d want 404", w.Code)
			}
		})
	}
}
