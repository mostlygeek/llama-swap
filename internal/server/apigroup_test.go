package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/cache"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/mostlygeek/llama-swap/internal/store"
)

func TestServer_InflightMiddleware_AddsAndRemovesEntriesAroundRequestHandling(t *testing.T) {
	tracker := newInflightTracker()
	mw := CreateInflightMiddleware(tracker)

	var duringRequest shared.InFlightRequestsEvent
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		duringRequest = tracker.Current()
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	started := time.Now().Add(-time.Second)
	req = req.WithContext(context.WithValue(req.Context(), inflightStartContextKey{}, started))
	req.Header.Set("User-Agent", "test-agent")
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	req = req.WithContext(shared.SetContext(req.Context(), shared.ReqContextData{
		Model:    "requested-model",
		ModelID:  "resolved-model",
		Metadata: map[string]string{"source": "test"},
	}))

	handler.ServeHTTP(httptest.NewRecorder(), req)

	if len(duringRequest.Requests) != 1 {
		t.Fatalf("inflight requests during request = %d, want 1", len(duringRequest.Requests))
	}
	entry := duringRequest.Requests[0]
	if entry.ID == "" || entry.Model != "resolved-model" || entry.Method != http.MethodPost || entry.ReqPath != "/v1/chat/completions" {
		t.Errorf("inflight entry = %+v", entry)
	}
	if !entry.Timestamp.Equal(started) {
		t.Errorf("timestamp = %v, want request start %v", entry.Timestamp, started)
	}
	if entry.ElapsedMs < 1000 {
		t.Errorf("elapsed ms = %d, want at least 1000", entry.ElapsedMs)
	}
	if entry.Metadata["source"] != "test" {
		t.Errorf("metadata = %v, want source=test", entry.Metadata)
	}
	if entry.RemoteIP != "203.0.113.9" {
		t.Errorf("remote ip = %q, want 203.0.113.9", entry.RemoteIP)
	}
	if entry.ReqHeaders["User-Agent"] != "test-agent" || entry.ReqHeaders["Authorization"] != "[REDACTED]" {
		t.Errorf("request headers = %v", entry.ReqHeaders)
	}
	if got := tracker.Current(); len(got.Requests) != 0 {
		t.Errorf("inflight after request = %+v, want empty", got)
	}
}

func TestServer_InflightMiddleware_StreamsResponseUpdates(t *testing.T) {
	events := make(chan shared.InFlightRequestsEvent, 8)
	tracker := newInflightTrackerWithPublisher(8, func(update shared.InFlightRequestsEvent) {
		events <- update
	})

	release := make(chan struct{})
	done := make(chan struct{})
	handler := CreateInflightMiddleware(tracker)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Set-Cookie", "secret=value")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
		w.(http.Flusher).Flush()
		<-release
	}))

	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req = req.WithContext(shared.SetContext(req.Context(), shared.ReqContextData{ModelID: "m1"}))
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()

	_ = waitInflightEvent(t, events, inflightOperationUpsert)
	headers := waitInflightEvent(t, events, inflightOperationUpsert)
	if headers.Request == nil || headers.Request.RespHeaders["Content-Type"] != "text/event-stream" {
		t.Fatalf("response headers event = %+v", headers)
	}
	if headers.Request.RespHeaders["Set-Cookie"] != "[REDACTED]" {
		t.Errorf("response headers = %v", headers.Request.RespHeaders)
	}

	bytesUpdate := waitInflightEvent(t, events, inflightOperationUpsert)
	if bytesUpdate.Request == nil || bytesUpdate.Request.RespBytes != 5 {
		t.Errorf("response bytes event = %+v, want 5", bytesUpdate.Request)
	}

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return")
	}
	removed := waitInflightEvent(t, events, inflightOperationRemove)
	if removed.ID == "" {
		t.Error("remove event missing id")
	}
}

func TestServer_InflightEventPayloadIncludesRequestEntries(t *testing.T) {
	events := make(chan shared.InFlightRequestsEvent, 4)
	tracker := newInflightTrackerWithPublisher(4, func(update shared.InFlightRequestsEvent) {
		events <- update
	})

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

	added := waitInflightEvent(t, events, inflightOperationUpsert)
	if added.Request == nil {
		t.Fatal("added request is nil")
	}
	if added.Request.Model != "m1" || added.Request.ReqPath != "/props" {
		t.Errorf("added request = %+v", added.Request)
	}

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return")
	}
	removed := waitInflightEvent(t, events, inflightOperationRemove)
	if removed.ID != added.Request.ID {
		t.Errorf("removed id = %q, want %q", removed.ID, added.Request.ID)
	}
}

func TestServer_InflightTracker_OutboxDoesNotBlockRequests(t *testing.T) {
	publishStarted := make(chan struct{})
	releasePublisher := make(chan struct{})
	published := make(chan shared.InFlightRequestsEvent, 4)
	var blockFirst sync.Once

	tracker := newInflightTrackerWithPublisher(1, func(update shared.InFlightRequestsEvent) {
		blockFirst.Do(func() {
			close(publishStarted)
			<-releasePublisher
		})
		published <- update
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	id := tracker.Add(req, func() {})
	select {
	case <-publishStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher did not start")
	}

	// Fill the one-item outbox while the publisher is blocked, then overflow
	// it with the removal. The request path must still return immediately.
	tracker.SetResponseHeaders(id, http.Header{"Content-Type": {"text/event-stream"}})
	removed := make(chan struct{})
	go func() {
		tracker.Remove(id)
		close(removed)
	}()
	select {
	case <-removed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Remove blocked on a busy publisher")
	}

	close(releasePublisher)
	_ = waitInflightEvent(t, published, inflightOperationUpsert)
	recovered := waitInflightEvent(t, published, inflightOperationSnapshot)
	if len(recovered.Requests) != 0 {
		t.Errorf("recovery snapshot = %+v, want no requests", recovered.Requests)
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
	waitInflightTrackerCount(t, tracker, 0)
}

func waitInflightTrackerCount(t *testing.T, tracker *inflightTracker, total int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if got := tracker.Current(); len(got.Requests) == total {
			return
		}
		select {
		case <-deadline:
			got := tracker.Current()
			t.Fatalf("inflight total = %d, want %d", len(got.Requests), total)
		case <-tick.C:
		}
	}
}

func waitInflightEvent(t *testing.T, events <-chan shared.InFlightRequestsEvent, operation string) shared.InFlightRequestsEvent {
	t.Helper()
	timer := time.After(2 * time.Second)
	for {
		select {
		case got := <-events:
			if got.Operation == operation {
				return got
			}
		case <-timer:
			t.Fatalf("timed out waiting for inflight operation %q", operation)
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

func TestServer_APIMetricsActivity_Empty(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/metrics/activity", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var page store.ActivityPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if page.Total != 0 || len(page.Data) != 0 {
		t.Errorf("page = %+v, want empty", page)
	}
}

func TestServer_APIMetricsActivity(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.metrics.enableCaptures = true
	s.metrics.captureCache = cache.New(1024 * 1024)

	storedM1, ok := s.metrics.queueMetrics(ActivityLogEntry{
		Timestamp: time.Unix(1, 0),
		Model:     "m1",
		ReqPath:   "/v1/chat/completions",
		Tokens:    TokenMetrics{InputTokens: 1, OutputTokens: 2},
	})
	if !ok {
		t.Fatal("queueMetrics m1 failed")
	}
	if ok := s.metrics.addCapture(ReqRespCapture{ID: storedM1.ID, ReqPath: "/v1/chat/completions"}); !ok {
		t.Fatal("addCapture failed")
	}
	if _, ok := s.metrics.queueMetrics(ActivityLogEntry{
		Timestamp: time.Unix(2, 0),
		Model:     "m2",
		ReqPath:   "/v1/chat/completions",
		Tokens:    TokenMetrics{InputTokens: 3, OutputTokens: 4},
	}); !ok {
		t.Fatal("queueMetrics m2 failed")
	}

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/metrics/activity?model=m1&limit=10&page=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", w.Code, w.Body.String())
	}
	var page store.ActivityPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if page.Total != 1 || len(page.Data) != 1 {
		t.Fatalf("page = %+v", page)
	}
	if page.Data[0].ID != storedM1.ID || !page.Data[0].HasCapture {
		t.Fatalf("entry = %+v", page.Data[0])
	}
}

func TestServer_APIMetricsStats(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	for _, entry := range []ActivityLogEntry{
		{Timestamp: time.Unix(1, 0), Model: "m1", Tokens: TokenMetrics{InputTokens: 1, OutputTokens: 2, CachedTokens: 1, PromptPerSecond: 10, TokensPerSecond: 20}},
		{Timestamp: time.Unix(2, 0), Model: "m1", Tokens: TokenMetrics{InputTokens: 3, OutputTokens: 4, PromptPerSecond: 30, TokensPerSecond: 40}},
		{Timestamp: time.Unix(3, 0), Model: "m2", Tokens: TokenMetrics{InputTokens: 5, OutputTokens: 6, PromptPerSecond: 50}},
	} {
		if _, ok := s.metrics.queueMetrics(entry); !ok {
			t.Fatal("queueMetrics failed")
		}
	}

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/metrics/stats?model=m1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", w.Code, w.Body.String())
	}
	var stats store.ActivityStats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stats.TotalRequests != 2 || stats.TotalInputTokens != 4 || stats.TotalOutputTokens != 6 || stats.TotalCacheTokens != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.PromptHistogram == nil || stats.GenerationHistogram == nil {
		t.Fatalf("expected histograms: %+v", stats)
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
	if got := s.inflight.Current(); len(got.Requests) != 0 {
		t.Fatalf("inflight total after request = %d, want 0", len(got.Requests))
	}
	gotMetrics := metricsEntries(t, s.metrics)
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
	s.cfg.UI.Activity.SessionID = []string{"X-Trace-ID"}

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
	for _, want := range []string{`"type":"modelStatus"`, `"type":"inflight"`, `"type":"uiConfig"`, `"type":"logData"`, `X-Trace-ID`} {
		if !strings.Contains(body, want) {
			t.Errorf("initial SSE payload missing %s; body=%q", want, body)
		}
	}
}
