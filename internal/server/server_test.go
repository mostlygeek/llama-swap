package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/router"
)

// stubRouter is a minimal router.Router for Server dispatch tests.
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

// newTestServer wires a Server with stub routers and a built mux.
func newTestServer(local, peer router.Router) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:         config.Config{},
		muxlog:      logmon.NewWriter(io.Discard),
		proxylog:    logmon.NewWriter(io.Discard),
		upstreamlog: logmon.NewWriter(io.Discard),
		local:       local,
		peer:        peer,
		shutdownCtx: ctx,
		shutdownFn:  cancel,
	}
	s.routes()
	return s
}

func chatRequest(model string) *http.Request {
	body := strings.NewReader(`{"model":"` + model + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestServer_New_GroupConfig(t *testing.T) {
	discard := logmon.NewWriter(io.Discard)
	s, err := New(config.Config{HealthCheckTimeout: 15}, discard, discard)
	if err != nil {
		t.Fatalf("New (group): %v", err)
	}
	if err := s.Shutdown(time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestServer_New_MatrixConfig(t *testing.T) {
	discard := logmon.NewWriter(io.Discard)
	cfg := config.Config{HealthCheckTimeout: 15, Matrix: &config.MatrixConfig{}}
	s, err := New(cfg, discard, discard)
	if err != nil {
		t.Fatalf("New (matrix): %v", err)
	}
	if err := s.Shutdown(time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestServer_RouteToLocalModel(t *testing.T) {
	s := newTestServer(
		newStubRouter([]string{"local-model"}, "local response"),
		newStubRouter(nil, ""),
	)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("local-model"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if w.Body.String() != "local response" {
		t.Errorf("body=%q want %q", w.Body.String(), "local response")
	}
}

func TestServer_RouteToPeerModel(t *testing.T) {
	s := newTestServer(
		newStubRouter(nil, ""),
		newStubRouter([]string{"peer-model"}, "peer response"),
	)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("peer-model"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if w.Body.String() != "peer response" {
		t.Errorf("body=%q want %q", w.Body.String(), "peer response")
	}
}

func TestServer_UnknownModelReturns404(t *testing.T) {
	s := newTestServer(
		newStubRouter([]string{"local-model"}, ""),
		newStubRouter(nil, ""),
	)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("unknown-model"))

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404 body=%q", w.Code, w.Body.String())
	}
}

func TestServer_UnknownPathReturns404(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/does-not-exist", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404", w.Code)
	}
}

func TestServer_Health(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	for _, path := range []string{"/health", "/wol-health"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || w.Body.String() != "OK" {
			t.Errorf("%s: status=%d body=%q", path, w.Code, w.Body.String())
		}
	}
}

func TestServer_CORSPreflight(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin=%q want *", got)
	}
}

func TestServer_Shutdown_StopsRoutersAndIsIdempotent(t *testing.T) {
	local := newStubRouter([]string{"local-model"}, "")
	peer := newStubRouter(nil, "")
	s := newTestServer(local, peer)

	if err := s.Shutdown(time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := s.Shutdown(time.Second); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	if got := local.shutdownCalls.Load(); got != 1 {
		t.Errorf("local shutdownCalls=%d want 1", got)
	}
	if got := peer.shutdownCalls.Load(); got != 1 {
		t.Errorf("peer shutdownCalls=%d want 1", got)
	}
}
