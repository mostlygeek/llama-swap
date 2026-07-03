package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

func TestServer_InflightMiddleware_AddsAndRemovesEntriesAroundRequestHandling(t *testing.T) {
	tracker := newInflightTracker()
	mw := CreateInflightMiddleware(tracker)

	var duringRequest shared.InFlightRequestsEvent
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		duringRequest = tracker.Current()
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(shared.SetContext(req.Context(), shared.ReqContextData{
		Model:    "requested-model",
		ModelID:  "resolved-model",
		Metadata: map[string]string{"source": "test"},
	}))

	handler.ServeHTTP(httptest.NewRecorder(), req)

	if duringRequest.Total != 1 {
		t.Fatalf("inflight total during request = %d, want 1", duringRequest.Total)
	}
	if len(duringRequest.Requests) != 1 {
		t.Fatalf("inflight requests during request = %d, want 1", len(duringRequest.Requests))
	}
	entry := duringRequest.Requests[0]
	if entry.ID == "" || entry.Model != "resolved-model" || entry.Method != http.MethodPost || entry.ReqPath != "/v1/chat/completions" {
		t.Errorf("inflight entry = %+v", entry)
	}
	if entry.Metadata["source"] != "test" {
		t.Errorf("metadata = %v, want source=test", entry.Metadata)
	}
	if got := tracker.Current(); got.Total != 0 || len(got.Requests) != 0 {
		t.Errorf("inflight after request = %+v, want empty", got)
	}
}

func TestServer_InflightEventPayloadIncludesRequestEntries(t *testing.T) {
	tracker := newInflightTracker()
	events := make(chan shared.InFlightRequestsEvent, 4)
	cancelEvents := event.On(func(e shared.InFlightRequestsEvent) { events <- e })
	defer cancelEvents()

	release := make(chan struct{})
	done := make(chan struct{})
	handler := CreateInflightMiddleware(tracker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))

	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "/props?model=m1", nil)
		req = req.WithContext(shared.SetContext(req.Context(), shared.ReqContextData{
			Model:   "m1",
			ModelID: "m1",
		}))
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()

	added := waitInflightEvent(t, events, 1)
	if len(added.Requests) != 1 {
		t.Fatalf("added requests = %d, want 1", len(added.Requests))
	}
	if added.Requests[0].Model != "m1" || added.Requests[0].ReqPath != "/props" {
		t.Errorf("added request = %+v", added.Requests[0])
	}

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return")
	}
	removed := waitInflightEvent(t, events, 0)
	if len(removed.Requests) != 0 {
		t.Errorf("removed requests = %d, want 0", len(removed.Requests))
	}
}

func TestServer_InflightCancelByIDCancelsRequestContext(t *testing.T) {
	tracker := newInflightTracker()
	idCh := make(chan string, 1)
	done := make(chan struct{})
	handler := CreateInflightMiddleware(tracker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := tracker.Current()
		if len(current.Requests) != 1 {
			t.Errorf("inflight requests = %d, want 1", len(current.Requests))
			return
		}
		idCh <- current.Requests[0].ID
		<-r.Context().Done()
		close(done)
	}))

	go func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req = req.WithContext(shared.SetContext(req.Context(), shared.ReqContextData{ModelID: "m1"}))
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()

	var id string
	select {
	case id = <-idCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inflight id")
	}
	if !tracker.Cancel(id) {
		t.Fatalf("Cancel(%q) = false, want true", id)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("request context was not canceled")
	}
	if got := tracker.Current(); got.Total != 0 {
		t.Errorf("inflight after cancel cleanup = %d, want 0", got.Total)
	}
}

func waitInflightEvent(t *testing.T, events <-chan shared.InFlightRequestsEvent, total int) shared.InFlightRequestsEvent {
	t.Helper()
	timer := time.After(2 * time.Second)
	for {
		select {
		case got := <-events:
			if got.Total == total {
				return got
			}
		case <-timer:
			t.Fatalf("timed out waiting for inflight total %d", total)
		}
	}
}

func TestServer_APIVersion(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.build = BuildInfo{Version: "1.2.3", Commit: "deadbeef", Date: "2026-05-19"}

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/version", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["version"] != "1.2.3" || got["commit"] != "deadbeef" || got["build_date"] != "2026-05-19" {
		t.Errorf("body = %v", got)
	}
}

func TestServer_APIMetrics_Empty(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != "[]" {
		t.Errorf("body = %q, want []", body)
	}
}

func TestServer_APICancelInflight(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	ctx, cancel := context.WithCancel(context.Background())
	id := s.inflight.Add(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx), cancel)
	defer s.inflight.Remove(id)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/inflight/"+id+"/cancel", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%q", w.Code, w.Body.String())
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("cancel endpoint did not cancel request context")
	}

	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/inflight/missing/cancel", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404", w.Code)
	}
}

func TestServer_InflightMetricsRecordsCompletedOnce(t *testing.T) {
	local := newStubRouter([]string{"m1"}, `{"usage":{"prompt_tokens":1,"completion_tokens":2}}`)
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = configWithModels("m1")

	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("m1"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", w.Code, w.Body.String())
	}
	if got := s.inflight.Current(); got.Total != 0 {
		t.Fatalf("inflight total after request = %d, want 0", got.Total)
	}
	gotMetrics := s.metrics.getMetrics()
	if len(gotMetrics) != 1 {
		t.Fatalf("metrics len = %d, want 1", len(gotMetrics))
	}
	if gotMetrics[0].Model != "m1" || gotMetrics[0].Tokens.InputTokens != 1 || gotMetrics[0].Tokens.OutputTokens != 2 {
		t.Errorf("metric = %+v", gotMetrics[0])
	}
}

func configWithModels(models ...string) config.Config {
	cfg := config.Config{Models: make(map[string]config.ModelConfig, len(models))}
	for _, model := range models {
		cfg.Models[model] = config.ModelConfig{}
	}
	return cfg
}

func TestServer_APIPerformance_Unavailable(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/performance", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestServer_APIEvents_InitialPayload(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		s.ServeHTTP(w, req)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancel")
	}

	body := w.Body.String()
	for _, want := range []string{`"type":"modelStatus"`, `"type":"inflight"`, `"type":"logData"`} {
		if !strings.Contains(body, want) {
			t.Errorf("initial SSE payload missing %s; body=%q", want, body)
		}
	}
}
