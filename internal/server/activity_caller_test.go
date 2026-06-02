package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"gopkg.in/yaml.v3"
)

func TestMaskCaller(t *testing.T) {
	cases := map[string]string{
		"":          "anonymous",
		"sk-ragtag": "sk-ra…9",
		"sk-aw3":    "sk-aw…6",
		"abc":       "abc", // too short to be a usable secret; passthrough
	}
	for in, want := range cases {
		if got := maskCaller(in); got != want {
			t.Errorf("maskCaller(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRecordRejection_AppearsInActivity verifies a scheduler 429 lands in the
// activity log (masked caller, 429 status) so it shows in the trace view.
// With captures disabled (buffer 0), no capture is stored.
func TestRecordRejection_AppearsInActivity(t *testing.T) {
	mm := newMetricsMonitor(logmon.NewWriter(io.Discard), 10, 0)
	hdrs := map[string]string{"X-RateLimit-Limit": "1"}
	body := []byte(`{"reason":"queue_full","hint":{"max_concurrency":1}}`)
	mm.recordRejection("nomic-embed-text", "sk-ragtag", "/v1/embeddings", 429, hdrs, body)

	got := mm.getMetrics()
	if len(got) != 1 {
		t.Fatalf("want 1 activity entry, got %d", len(got))
	}
	e := got[0]
	if e.RespStatusCode != 429 {
		t.Errorf("status = %d, want 429", e.RespStatusCode)
	}
	if e.Model != "nomic-embed-text" || e.ReqPath != "/v1/embeddings" {
		t.Errorf("model/path = %q/%q", e.Model, e.ReqPath)
	}
	if e.Caller != "sk-ra…9" {
		t.Errorf("caller = %q, want masked sk-ra…9", e.Caller)
	}
	if e.HasCapture {
		t.Error("captures disabled (buffer 0): HasCapture should be false")
	}
}

// With captures enabled, a 429 stores a hint-only capture (response side only,
// no request body) retrievable by ID.
func TestRecordRejection_CapturesHint(t *testing.T) {
	mm := newMetricsMonitor(logmon.NewWriter(io.Discard), 10, 1) // 1 MB capture buffer
	hdrs := map[string]string{"X-RateLimit-Limit": "1", "Retry-After": "5"}
	body := []byte(`{"reason":"queue_full","hint":{"max_concurrency":1}}`)
	mm.recordRejection("m1", "sk-ragtag", "/v1/embeddings", 429, hdrs, body)

	got := mm.getMetrics()
	if len(got) != 1 || !got[0].HasCapture {
		t.Fatalf("expected one entry with HasCapture=true, got %+v", got)
	}
	capture := mm.getCaptureByID(got[0].ID)
	if capture == nil {
		t.Fatal("capture not retrievable by ID")
	}
	if string(capture.RespBody) != string(body) {
		t.Errorf("capture resp body = %q, want %q", capture.RespBody, body)
	}
	if capture.RespHeaders["X-RateLimit-Limit"] != "1" {
		t.Errorf("capture resp headers missing the hint: %v", capture.RespHeaders)
	}
	if len(capture.ReqBody) != 0 {
		t.Errorf("hint-only capture must omit the request body, got %d bytes", len(capture.ReqBody))
	}
}

// nil metricsMonitor must not panic (Server built without New()).
func TestRecordRejection_NilSafe(t *testing.T) {
	var mm *metricsMonitor
	mm.recordRejection("m", "c", "/p", 429, nil, nil) // must not panic
}

// TestCallerMiddleware_ResolvesPriorityFromBearer closes the seam between the
// caller middleware and the scheduler: a Bearer key on the request must surface
// as the caller id in context, which the scheduling config then maps to the
// caller's configured priority.
func TestCallerMiddleware_ResolvesPriorityFromBearer(t *testing.T) {
	cfg := config.Config{}
	if err := yaml.Unmarshal([]byte("priorities:\n  sk-vip: 9\ndefaultPriority: 3\n"), &cfg.Scheduling); err != nil {
		t.Fatalf("unmarshal scheduling: %v", err)
	}

	var gotPriority int
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPriority = cfg.Scheduling.PriorityFor(callerFromContext(r.Context()))
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-vip")
	CreateCallerMiddleware()(inner).ServeHTTP(httptest.NewRecorder(), req)

	if gotPriority != 9 {
		t.Fatalf("Bearer sk-vip resolved to priority %d, want 9", gotPriority)
	}
}
