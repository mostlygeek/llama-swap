package server

import (
	"context"
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
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/router"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/mostlygeek/llama-swap/internal/store"
)

// stubRouter is a minimal router.LocalRouter for Server dispatch tests.
type stubRouter struct {
	models        map[string]bool
	response      string
	serveHTTP     func(http.ResponseWriter, *http.Request)
	shutdownCalls atomic.Int32
	running       map[string]process.ProcessState
	unloadCalls   atomic.Int32
	unloadModels  []string
	unloadTimeout time.Duration
	loggers       map[string]*logmon.Monitor
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
func (s *stubRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.serveHTTP != nil {
		s.serveHTTP(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(s.response))
}

func (s *stubRouter) RunningModels() map[string]process.ProcessState { return s.running }
func (s *stubRouter) Unload(timeout time.Duration, models ...string) {
	s.unloadCalls.Add(1)
	s.unloadTimeout = timeout
	s.unloadModels = append([]string(nil), models...)
}
func (s *stubRouter) ProcessLogger(modelID string) (*logmon.Monitor, bool) {
	if s.loggers != nil {
		if lg, ok := s.loggers[modelID]; ok {
			return lg, true
		}
	}
	return nil, false
}

// newTestServer wires a Server with stub routers and a built mux.
func newTestServer(local router.LocalRouter, peer router.Router) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	proxylog := logmon.NewWriter(io.Discard)
	st, err := store.New("")
	if err != nil {
		panic(err)
	}
	s := &Server{
		cfg:         config.Config{},
		muxlog:      logmon.NewWriter(io.Discard),
		proxylog:    proxylog,
		upstreamlog: logmon.NewWriter(io.Discard),
		inflight:    newInflightTracker(),
		metrics:     newMetricsMonitor(proxylog, 0, 0, st),
		store:       st,
		local:       local,
		peer:        peer,
		shutdownCtx: ctx,
		shutdownFn:  cancel,
	}
	s.routes()
	return s
}

func newTestMetricsMonitor(t *testing.T, logger *logmon.Monitor, maxMetrics int, captureBufferMB int) *metricsMonitor {
	t.Helper()
	st, err := store.New("")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	return newMetricsMonitor(logger, maxMetrics, captureBufferMB, st)
}

func metricsEntries(t *testing.T, mm *metricsMonitor) []ActivityLogEntry {
	t.Helper()
	page, err := mm.store.ListActivity(context.Background(), store.ActivityQuery{Limit: 1000, Page: 1})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	mm.overlayCaptureState(page.Data)
	return page.Data
}

func chatRequest(model string) *http.Request {
	body := strings.NewReader(`{"model":"` + model + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestServer_New_GroupConfig(t *testing.T) {
	discard := logmon.NewWriter(io.Discard)
	cfg := config.Config{HealthCheckTimeout: 15}
	cfg.Routing.Router.Use = "group"
	st, err := store.New("")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()
	s, err := New(cfg, discard, discard, discard, nil, st, BuildInfo{})
	if err != nil {
		t.Fatalf("New (group): %v", err)
	}
	if _, ok := s.local.(*router.Group); !ok {
		t.Fatalf("localRouter=%T want *router.Group", s.local)
	}
	if err := s.Shutdown(time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestServer_New_MatrixConfig(t *testing.T) {
	discard := logmon.NewWriter(io.Discard)
	cfg := config.Config{HealthCheckTimeout: 15}
	cfg.Routing.Router.Use = "matrix"
	cfg.Routing.Router.Settings.Matrix = &config.MatrixConfig{}
	st, err := store.New("")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()
	s, err := New(cfg, discard, discard, discard, nil, st, BuildInfo{})
	if err != nil {
		t.Fatalf("New (matrix): %v", err)
	}
	if _, ok := s.local.(*router.Matrix); !ok {
		t.Fatalf("localRouter=%T want *router.Matrix", s.local)
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

func TestServer_Unload(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "")
	s := newTestServer(local, newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/unload", nil))

	if w.Code != http.StatusOK || w.Body.String() != "OK" {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := local.unloadCalls.Load(); got != 1 {
		t.Errorf("unloadCalls=%d want 1", got)
	}
	if len(local.unloadModels) != 0 {
		t.Errorf("unloadModels=%v want empty for unload all", local.unloadModels)
	}
	if local.unloadTimeout != 0 {
		t.Errorf("unloadTimeout=%v want 0 (use configured timeouts)", local.unloadTimeout)
	}
}

func TestServer_Running(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "")
	local.running = map[string]process.ProcessState{"m1": process.StateReady}
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{
		"m1": {
			Cmd:         "llama-server",
			Proxy:       "http://localhost:9999",
			UnloadAfter: 300,
			Name:        "Model One",
			Description: "the first model",
		},
	}}

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/running", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}

	var resp struct {
		Running []runningModel `json:"running"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%q", err, w.Body.String())
	}
	if len(resp.Running) != 1 {
		t.Fatalf("running=%v want 1 entry", resp.Running)
	}
	want := runningModel{
		Model:       "m1",
		State:       "ready",
		Cmd:         "llama-server",
		Proxy:       "http://localhost:9999",
		TTL:         300,
		Name:        "Model One",
		Description: "the first model",
	}
	if resp.Running[0] != want {
		t.Errorf("got %+v want %+v", resp.Running[0], want)
	}
}

func TestServer_Preload(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "ok")
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Hooks: config.HooksConfig{
		OnStartup: config.HookOnStartup{Preload: []string{"m1"}},
	}}

	got := make(chan shared.ModelPreloadedEvent, 1)
	cancel := event.On(func(e shared.ModelPreloadedEvent) { got <- e })
	defer cancel()

	s.startPreload()

	select {
	case e := <-got:
		if e.ModelName != "m1" || !e.Success {
			t.Errorf("event=%+v want {ModelName:m1 Success:true}", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preload event not received")
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

func TestServer_LogStream_ModelID(t *testing.T) {
	buf := logmon.NewWriter(io.Discard)
	buf.Write([]byte("hello from model"))

	local := newStubRouter([]string{"mymodel"}, "")
	local.loggers = map[string]*logmon.Monitor{"mymodel": buf}

	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{"mymodel": {}}}

	// Pre-cancel the context so the streaming loop exits immediately after
	// flushing history.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/logs/stream/mymodel", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "hello from model" {
		t.Errorf("body=%q want %q", got, "hello from model")
	}
}

func TestServer_LogStream_UnknownID_Returns400(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/logs/stream/no-such-model", nil))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", w.Code)
	}
}
