package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/store/sqlite"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// pauseServer posts to /api/models/pause and checks the response.
func pauseServer(t *testing.T, s *Server) {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/models/pause", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("pause status=%d body=%q", w.Code, w.Body.String())
	}
	var body map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || !body["paused"] {
		t.Fatalf("pause body=%q want {\"paused\":true}", w.Body.String())
	}
}

// assertPausedResponse checks w holds the 503 a paused server returns.
func assertPausedResponse(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503; body=%q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), errInferencePausedMessage) {
		t.Errorf("body=%q want it to contain %q", w.Body.String(), errInferencePausedMessage)
	}
}

func TestServer_PauseUnloadsModels(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "")
	s := newTestServer(local, newStubRouter(nil, ""))

	got := make(chan swaputil.InferencePausedEvent, 1)
	cancel := event.On(func(e swaputil.InferencePausedEvent) { got <- e })
	defer cancel()

	pauseServer(t, s)

	if n := local.unloadCalls.Load(); n != 1 {
		t.Errorf("unloadCalls=%d want 1", n)
	}
	if len(local.unloadModels) != 0 {
		t.Errorf("unloadModels=%v want empty for unload all", local.unloadModels)
	}
	select {
	case e := <-got:
		if !e.Paused {
			t.Errorf("event=%+v want Paused=true", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pause event not received")
	}
}

func TestServer_PauseRejectsLocalInference(t *testing.T) {
	var served atomic.Int32
	local := newStubRouter([]string{"m1"}, "")
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) { served.Add(1) }
	s := newTestServer(local, newStubRouter(nil, ""))

	pauseServer(t, s)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("m1"))
	assertPausedResponse(t, w)
	if n := served.Load(); n != 0 {
		t.Errorf("local router served %d requests while paused, want 0", n)
	}
}

func TestServer_PauseAllowsPeerInference(t *testing.T) {
	s := newTestServer(
		newStubRouter([]string{"m1"}, ""),
		newStubRouter([]string{"peer-model"}, "peer response"),
	)

	pauseServer(t, s)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("peer-model"))
	if w.Code != http.StatusOK || w.Body.String() != "peer response" {
		t.Fatalf("status=%d body=%q want 200 peer response", w.Code, w.Body.String())
	}
}

func TestServer_PauseRejectsUpstream(t *testing.T) {
	s := newTestServerWithConfig(
		config.Config{Models: map[string]config.ModelConfig{"m1": {}}},
		newStubRouter([]string{"m1"}, "ok"),
		newStubRouter(nil, ""),
	)

	pauseServer(t, s)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/m1/health", nil))
	assertPausedResponse(t, w)
}

func TestServer_PauseRejectsComfyUI(t *testing.T) {
	s := newTestServerWithConfig(
		config.Config{Models: map[string]config.ModelConfig{config.ComfyUIModelID: {}}},
		newStubRouter([]string{config.ComfyUIModelID}, "ok"),
		newStubRouter(nil, ""),
	)

	pauseServer(t, s)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/comfyui/prompt", strings.NewReader("{}")))
	assertPausedResponse(t, w)
}

func TestServer_ResumeAllowsInference(t *testing.T) {
	s := newTestServer(newStubRouter([]string{"m1"}, "local response"), newStubRouter(nil, ""))

	pauseServer(t, s)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/models/resume", nil))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"paused":false}` {
		t.Fatalf("resume status=%d body=%q", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("m1"))
	if w.Code != http.StatusOK || w.Body.String() != "local response" {
		t.Fatalf("status=%d body=%q want 200 local response", w.Code, w.Body.String())
	}
}

func TestServer_PauseSkipsPreload(t *testing.T) {
	var served atomic.Int32
	local := newStubRouter([]string{"m1"}, "ok")
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) { served.Add(1) }
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Hooks: config.HooksConfig{
		OnStartup: config.HookOnStartup{Preload: []string{"m1"}},
	}}
	s.pause.set(true)

	got := make(chan swaputil.ModelPreloadedEvent, 1)
	cancel := event.On(func(e swaputil.ModelPreloadedEvent) { got <- e })
	defer cancel()

	s.startPreload()

	select {
	case e := <-got:
		t.Fatalf("preload ran while paused: %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
	if n := served.Load(); n != 0 {
		t.Errorf("local router served %d preload requests while paused, want 0", n)
	}
}

// TestServer_PauseSurvivesReload checks that a Server built with an already
// paused PauseState, as a hot config reload does, keeps rejecting requests.
func TestServer_PauseSurvivesReload(t *testing.T) {
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
models:
  m1:
    cmd: echo ready
    proxy: http://localhost:8080
`))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	st, err := sqlite.New("")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	defer st.Close()

	pause := NewPauseState()
	pause.set(true)
	s, err := New(cfg, logmon.NewGroup(io.Discard, true, true, true), nil, st, BuildInfo{}, nil, nil, nil, pause)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Shutdown(time.Second)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("m1"))
	assertPausedResponse(t, w)
}
