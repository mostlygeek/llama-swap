package router

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// stubRouter is a minimal Router implementation for Server routing tests.
type stubRouter struct {
	models        map[string]bool
	response      string
	shutdownCalls atomic.Int32
}

func newStubRouter(models []string, response string) *stubRouter {
	m := make(map[string]bool, len(models))
	for _, id := range models {
		m[id] = true
	}
	return &stubRouter{models: m, response: response}
}

func (s *stubRouter) Handles(model string) bool      { return s.models[model] }
func (s *stubRouter) Shutdown(_ time.Duration) error { s.shutdownCalls.Add(1); return nil }
func (s *stubRouter) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(s.response))
}

func newTestServerDirect(peer Router, router Router) *Server {
	return &Server{
		cfg:    config.Config{HealthCheckTimeout: 5},
		logger: testLogger,
		peer:   peer,
		router: router,
	}
}

func TestServer_RouteToLocalModel(t *testing.T) {
	peer := newStubRouter(nil, "")
	local := newStubRouter([]string{"local-model"}, "local response")
	s := newTestServerDirect(peer, local)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, newRequest("local-model"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if w.Body.String() != "local response" {
		t.Errorf("body=%q want %q", w.Body.String(), "local response")
	}
}

func TestServer_RouteToPeerModel(t *testing.T) {
	peer := newStubRouter([]string{"peer-model"}, "peer response")
	local := newStubRouter([]string{"local-model"}, "local response")
	s := newTestServerDirect(peer, local)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, newRequest("peer-model"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if w.Body.String() != "peer response" {
		t.Errorf("body=%q want %q", w.Body.String(), "peer response")
	}
}

// Peer takes priority when both peer and local claim the same model ID.
func TestServer_PeerTakesPrecedenceOverLocal(t *testing.T) {
	peer := newStubRouter([]string{"shared-model"}, "peer response")
	local := newStubRouter([]string{"shared-model"}, "local response")
	s := newTestServerDirect(peer, local)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, newRequest("shared-model"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if w.Body.String() != "peer response" {
		t.Errorf("body=%q want %q (peer should win)", w.Body.String(), "peer response")
	}
}

func TestServer_ModelNotFound(t *testing.T) {
	s := newTestServerDirect(newStubRouter(nil, ""), newStubRouter([]string{"local-model"}, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, newRequest("unknown-model"))

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404 body=%q", w.Code, w.Body.String())
	}
}

func TestServer_NoModelInRequest(t *testing.T) {
	s := newTestServerDirect(newStubRouter(nil, ""), newStubRouter(nil, ""))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404 body=%q", w.Code, w.Body.String())
	}
}

func TestServer_Handles(t *testing.T) {
	s := newTestServerDirect(
		newStubRouter([]string{"peer-model"}, ""),
		newStubRouter([]string{"local-model"}, ""),
	)

	tests := []struct {
		model string
		want  bool
	}{
		{"local-model", true},
		{"peer-model", true},
		{"unknown-model", false},
	}
	for _, tc := range tests {
		if got := s.Handles(tc.model); got != tc.want {
			t.Errorf("Handles(%q)=%v want %v", tc.model, got, tc.want)
		}
	}
}

func TestServer_Shutdown_StopsAllRouters(t *testing.T) {
	peer := newStubRouter(nil, "")
	local := newStubRouter([]string{"local-model"}, "")
	s := newTestServerDirect(peer, local)

	if err := s.Shutdown(time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := peer.shutdownCalls.Load(); got != 1 {
		t.Errorf("peer shutdownCalls=%d want 1", got)
	}
	if got := local.shutdownCalls.Load(); got != 1 {
		t.Errorf("local shutdownCalls=%d want 1", got)
	}
}
