package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

func TestServer_HandleComfyUI(t *testing.T) {
	local := newStubRouter([]string{config.ComfyUIModelID}, "")
	var gotPath string
	var gotQuery string
	var gotContext swaputil.ReqContextData
	serveCalls := 0
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		serveCalls++
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		gotContext, _ = swaputil.ReadContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{config.ComfyUIModelID: {}}}
	s.routes()

	t.Run("redirects bare path", func(t *testing.T) {
		for _, tt := range []struct {
			method string
			status int
		}{
			{method: http.MethodGet, status: http.StatusMovedPermanently},
			{method: http.MethodHead, status: http.StatusMovedPermanently},
			{method: http.MethodPost, status: http.StatusPermanentRedirect},
		} {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(tt.method, "/comfyui?token=value", nil))
			if w.Code != tt.status {
				t.Errorf("%s status=%d want %d", tt.method, w.Code, tt.status)
			}
			if got := w.Header().Get("Location"); got != "/comfyui/?token=value" {
				t.Errorf("%s Location=%q want /comfyui/?token=value", tt.method, got)
			}
		}
	})

	t.Run("unloaded model ignores only GETs to ignored paths", func(t *testing.T) {
		local.running = nil

		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/comfyui/?token=value", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("root status=%d want 200 body=%q", w.Code, w.Body.String())
		}
		if gotPath != "/" || gotQuery != "token=value" {
			t.Errorf("root path=%q query=%q want path=/ query=token=value", gotPath, gotQuery)
		}

		// Paths outside the ignore list, and any write, may start the model.
		for _, tt := range []struct {
			name   string
			method string
			target string
		}{
			{name: "api path", method: http.MethodGet, target: "/comfyui/api/prompt"},
			{name: "html asset", method: http.MethodGet, target: "/comfyui/index.html"},
			{name: "ws name prefix", method: http.MethodGet, target: "/comfyui/wsapi"},
			{name: "jobs sub path", method: http.MethodGet, target: "/comfyui/api/jobs/12"},
			{name: "non-GET ws", method: http.MethodPost, target: "/comfyui/ws"},
			{name: "non-GET ws sub path", method: http.MethodPost, target: "/comfyui/ws/sub"},
			{name: "non-GET jobs", method: http.MethodPost, target: "/comfyui/api/jobs"},
			{name: "non-GET asset", method: http.MethodPost, target: "/comfyui/assets/app.js"},
		} {
			before := serveCalls
			w = httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(tt.method, tt.target, nil))
			if w.Code != http.StatusOK {
				t.Errorf("%s status=%d want 200 body=%q", tt.name, w.Code, w.Body.String())
			}
			if serveCalls != before+1 {
				t.Errorf("%s did not reach the model router", tt.name)
			}
		}

		// /ws matches as a path prefix, so sub paths and query parameters
		// are ignored the same way. Static assets and /api/jobs come from
		// the same list the /upstream equivalent is configured with.
		targets := []string{
			"/comfyui/ws",
			"/comfyui/ws?clientId=abc123",
			"/comfyui/ws/",
			"/comfyui/ws/sub/path?a=1",
			"/comfyui/api/jobs",
			"/comfyui/api/jobs?open=1",
			"/comfyui/assets/app.js",
			"/comfyui/user/workflow.json",
			"/comfyui/style.css",
		}
		for _, state := range []map[string]process.ProcessState{
			nil,
			{config.ComfyUIModelID: process.StateStarting},
		} {
			local.running = state
			for _, target := range targets {
				before := serveCalls
				w = httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
				if w.Code != http.StatusConflict {
					t.Fatalf("%s status=%d want 409 body=%q running=%v", target, w.Code, w.Body.String(), state)
				}
				if serveCalls != before {
					t.Fatalf("unready model received %s, running=%v", target, state)
				}
				if !strings.Contains(w.Body.String(), "ignored ComfyUI paths cannot start it") {
					t.Errorf("%s body=%q missing ignored-path explanation", target, w.Body.String())
				}
			}
		}
	})

	t.Run("ready model proxies ignored paths", func(t *testing.T) {
		local.running = map[string]process.ProcessState{config.ComfyUIModelID: process.StateReady}
		before := serveCalls
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/comfyui/ws", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d want 200 body=%q", w.Code, w.Body.String())
		}
		if serveCalls != before+1 || gotPath != "/ws" {
			t.Errorf("serveCalls=%d path=%q want one call to /ws", serveCalls-before, gotPath)
		}
	})

	t.Run("proxies fixed model and preserves escaped path", func(t *testing.T) {
		local.running = map[string]process.ProcessState{config.ComfyUIModelID: process.StateReady}
		for _, tt := range []struct {
			name      string
			target    string
			wantPath  string
			wantQuery string
		}{
			{
				name:      "encoded slash",
				target:    "/comfyui/api/userdata/workflows%2Fexample.json?preview=1",
				wantPath:  "/api/userdata/workflows%2Fexample.json",
				wantQuery: "preview=1",
			},
			{
				name:     "double encoded slash",
				target:   "/comfyui/api/userdata/workflows%252Fexample.json",
				wantPath: "/api/userdata/workflows%252Fexample.json",
			},
			{
				name:     "utf8 and encoded slash",
				target:   "/comfyui/api/%E2%9C%93%2Ffile.json",
				wantPath: "/api/%E2%9C%93%2Ffile.json",
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, tt.target, nil)
				s.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
				}
				if gotPath != tt.wantPath {
					t.Errorf("path=%q want %q", gotPath, tt.wantPath)
				}
				if gotQuery != tt.wantQuery {
					t.Errorf("query=%q want %q", gotQuery, tt.wantQuery)
				}
			})
		}
		if gotContext.Model != config.ComfyUIModelID || gotContext.ModelID != config.ComfyUIModelID {
			t.Errorf("context=%+v want model %s", gotContext, config.ComfyUIModelID)
		}
	})
}

func TestServer_HandleComfyUI_RequiresExactLocalModel(t *testing.T) {
	tests := []struct {
		name  string
		cfg   config.Config
		local *stubRouter
		peer  *stubRouter
	}{
		{
			name:  "missing model",
			cfg:   config.Config{},
			local: newStubRouter(nil, ""),
			peer:  newStubRouter(nil, ""),
		},
		{
			name:  "peer model",
			cfg:   config.Config{Models: map[string]config.ModelConfig{}},
			local: newStubRouter(nil, ""),
			peer:  newStubRouter([]string{config.ComfyUIModelID}, "peer"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(tt.local, tt.peer)
			s.cfg = tt.cfg
			s.routes()
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/comfyui/", nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status=%d want 404 body=%q", w.Code, w.Body.String())
			}
		})
	}
}

func TestServer_HandleComfyUI_UsesAuthentication(t *testing.T) {
	local := newStubRouter([]string{config.ComfyUIModelID}, "ok")
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{
		RequiredAPIKeys: []string{"secret"},
		Models:          map[string]config.ModelConfig{config.ComfyUIModelID: {}},
	}
	s.routes()

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/comfyui/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d want 401", w.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/comfyui/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated status=%d want 200 body=%q", w.Code, w.Body.String())
	}
}

// TestServer_ComfyUIIgnorePaths pins the built-in list. It is documented in
// docs/kb/guides/upstreams/comfyui.md as the upstream.ignorePaths a second
// ComfyUI instance needs, so the two must not drift apart.
func TestServer_ComfyUIIgnorePaths(t *testing.T) {
	want := []string{
		config.DefaultUpstreamIgnorePathsPattern,
		`^/ws(/|$)`,
		`^/api/jobs$`,
	}
	if len(comfyUIIgnorePaths) != len(want) {
		t.Fatalf("got %d patterns, want %d", len(comfyUIIgnorePaths), len(want))
	}
	for i, pattern := range want {
		if got := comfyUIIgnorePaths[i].String(); got != pattern {
			t.Errorf("pattern %d = %q, want %q", i, got, pattern)
		}
	}

	for _, tt := range []struct {
		path string
		want bool
	}{
		{path: "/", want: false},
		{path: "/ws", want: true},
		{path: "/ws/", want: true},
		{path: "/ws/sub", want: true},
		{path: "/wsapi", want: false},
		{path: "/api/ws", want: false},
		{path: "/api/jobs", want: true},
		{path: "/api/jobs/12", want: false},
		{path: "/api/prompt", want: false},
		{path: "/assets/app.js", want: true},
		{path: "/user/workflow.json", want: true},
		{path: "/index.html", want: false},
	} {
		if got := comfyUIIgnoresPath(tt.path); got != tt.want {
			t.Errorf("comfyUIIgnoresPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
