package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_ActivitySourceStripsClientPort(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{name: "IPv4", remoteAddr: "192.168.1.10:54321", want: "ip:192.168.1.10"},
		{name: "IPv6", remoteAddr: "[2001:db8::10]:54321", want: "ip:2001:db8::10"},
		{name: "missing port", remoteAddr: "localhost", want: "ip:localhost"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = test.remoteAddr
			if got := activitySource(r); got != test.want {
				t.Fatalf("activitySource() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestServer_ActivitySourceForwardedHeaders(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*http.Request)
		want  string
	}{
		{"x-forwarded-for", func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.1")
		}, "xff:203.0.113.10"},
		{"x-real-ip", func(r *http.Request) {
			r.Header.Set("X-Real-IP", "203.0.113.20")
		}, "xff:203.0.113.20"},
		{"x-forwarded-for takes priority over x-real-ip", func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "203.0.113.10")
			r.Header.Set("X-Real-IP", "203.0.113.20")
		}, "xff:203.0.113.10"},
		{"x-forwarded-for whitespace-only first entry falls back to x-real-ip", func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "   , 10.0.0.1")
			r.Header.Set("X-Real-IP", "203.0.113.20")
		}, "xff:203.0.113.20"},
		{"x-forwarded-for empty first entry falls back to remote addr", func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", ",10.0.0.1")
		}, "ip:192.168.1.10"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = "192.168.1.10:54321"
			test.setup(r)
			if got := activitySource(r); got != test.want {
				t.Fatalf("activitySource() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestServer_MetricsMiddleware_OMLX(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "JSON"
		if streaming {
			name = "SSE"
		}
		t.Run(name, func(t *testing.T) {
			mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 10, 0)
			cfg := config.Config{Models: map[string]config.ModelConfig{"m": {}}}
			body := `{"usage":{"prompt_tokens":56,"completion_tokens":8,"prompt_tokens_per_second":63.74,"generation_tokens_per_second":66.38}}`
			contentType := "application/json"
			if streaming {
				contentType = "text/event-stream"
				body = "data: " + body + "\n\ndata: [DONE]\n\n"
			}
			handler := chain.New(CreateMetricsMiddleware(mm, cfg)).ThenFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", contentType)
				io.WriteString(w, body)
			})
			r := chatRequest("m")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusOK || w.Body.String() != body {
				t.Fatalf("response = %d %q, want 200 %q", w.Code, w.Body.String(), body)
			}
			entries := metricsEntries(t, mm)
			if len(entries) != 1 {
				t.Fatalf("want 1 entry, got %d", len(entries))
			}
			entry := entries[0]
			if entry.Model != "m" || entry.Tokens.InputTokens != 56 || entry.Tokens.OutputTokens != 8 || entry.Tokens.PromptPerSecond != 63.74 || entry.Tokens.TokensPerSecond != 66.38 {
				t.Fatalf("recorded activity = %+v", entry)
			}
		})
	}
}

func TestMetricsMonitor_RecordMetadata(t *testing.T) {
	mm := newTestMetricsMonitor(t, nil, 10, 0)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"usage":{}}`))
	r = r.WithContext(swaputil.SetContext(r.Context(), swaputil.ReqContextData{
		ModelID:  "m",
		Metadata: map[string]string{"client": "web", "trace": "abc"},
	}))

	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	copier.WriteHeader(http.StatusOK)
	copier.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`))

	mm.record("m", hint.TaskChat, r, copier, 0, nil, nil)

	entries := metricsEntries(t, mm)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Metadata["client"] != "web" {
		t.Errorf("client = %q, want web", entries[0].Metadata["client"])
	}
	if entries[0].Metadata["trace"] != "abc" {
		t.Errorf("trace = %q, want abc", entries[0].Metadata["trace"])
	}
}

// TestMetricsMonitor_RecordClientClosed covers #1029: a client that hangs up
// before a response is written must not be filed as a successful (empty-body)
// metric. It is recorded with the 499 sentinel and a client-cancelled
// ErrorMsg, and skips the capture the upstream-failure path would store.
func TestMetricsMonitor_RecordClientClosed(t *testing.T) {
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 10, 5)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	// Nothing is ever written: this is the cold-load cancellation shape.
	copier.MarkStatus(swaputil.StatusClientClosedRequest)

	mm.record("m", hint.TaskChat, r, copier, captureAll, []byte("req"), nil)

	entries := metricsEntries(t, mm)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.RespStatusCode != swaputil.StatusClientClosedRequest {
		t.Errorf("status = %d, want %d", entry.RespStatusCode, swaputil.StatusClientClosedRequest)
	}
	if entry.ErrorMsg != "client disconnected before response" {
		t.Errorf("error_msg = %q, want client-cancelled message", entry.ErrorMsg)
	}
	if entry.HasCapture {
		t.Error("a cancelled request has no response to capture")
	}
}

// TestServer_MetricsMiddleware_ClientClosed checks the middleware derives the
// sentinel for a handler that returns without writing, and that the marker
// propagates outward to the access-log recorder so both agree.
func TestServer_MetricsMiddleware_ClientClosed(t *testing.T) {
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 10, 0)
	cfg := config.Config{Models: map[string]config.ModelConfig{"m": {}}}

	proxylog := logmon.NewWriter(io.Discard)
	handler := chain.New(
		CreateRequestLogMiddleware(proxylog),
		CreateMetricsMiddleware(mm, cfg),
	).ThenFunc(func(w http.ResponseWriter, r *http.Request) {})

	ctx, cancel := context.WithCancel(context.Background())
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "192.168.1.1:5000"
	cancel()

	handler.ServeHTTP(httptest.NewRecorder(), r)

	entries := metricsEntries(t, mm)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].RespStatusCode != swaputil.StatusClientClosedRequest {
		t.Errorf("activity status = %d, want %d", entries[0].RespStatusCode, swaputil.StatusClientClosedRequest)
	}
	if line := string(proxylog.GetHistory()); !strings.Contains(line, "499 0") {
		t.Errorf("access log %q should report 499", line)
	}
}

// TestServer_MetricsMiddleware_ServerSideCancelIsNotClientClosed guards the
// distinction #1029's fix depends on. The inflight middleware derives a
// cancellable context that POST /api/inflight/{id}/cancel fires, so testing
// "is this request's context done?" would report an operator cancelling a
// request from the UI as a client that hung up.
func TestServer_MetricsMiddleware_ServerSideCancelIsNotClientClosed(t *testing.T) {
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 10, 0)
	cfg := config.Config{Models: map[string]config.ModelConfig{"m": {}}}

	proxylog := logmon.NewWriter(io.Discard)
	// The handler stands in for a dispatch that is cancelled mid-flight and
	// answers the still-connected client, as the proxy ErrorHandler does.
	handler := chain.New(
		CreateRequestLogMiddleware(proxylog),
		CreateMetricsMiddleware(mm, cfg),
	).ThenFunc(func(w http.ResponseWriter, r *http.Request) {
		derived, cancel := context.WithCancel(r.Context())
		defer cancel()
		cancel() // the operator cancels it
		r = r.WithContext(derived)
		if swaputil.MarkClientClosed(w, r) {
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	})

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "192.168.1.1:5000"

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadGateway {
		t.Errorf("client got %d, want %d: a connected client must be answered", w.Code, http.StatusBadGateway)
	}
	entries := metricsEntries(t, mm)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].RespStatusCode == swaputil.StatusClientClosedRequest {
		t.Error("a server-side cancel must not be recorded as a client disconnect")
	}
	if line := string(proxylog.GetHistory()); strings.Contains(line, "499") {
		t.Errorf("access log %q should not report 499 for a connected client", line)
	}
}

func TestMetricsMonitor_RecordFailedRequestCapture(t *testing.T) {
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 10, 5)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	reqHeaders := map[string]string{"content-type": "application/json"}

	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	copier.Header().Set("Content-Type", "application/json")
	copier.WriteHeader(http.StatusBadGateway)
	copier.Write([]byte(`{"error":{"message":"model unavailable"}}`))

	reqBody := []byte(`{"model":"m","messages":[]}`)
	mm.record("m", hint.TaskChat, r, copier, captureAll, reqBody, reqHeaders)

	entries := metricsEntries(t, mm)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.RespStatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", entry.RespStatusCode, http.StatusBadGateway)
	}
	if entry.ErrorMsg != "model unavailable" {
		t.Errorf("error_msg = %q, want extracted message", entry.ErrorMsg)
	}
	if !entry.HasCapture {
		t.Fatal("failed request should capture the request so it can be inspected")
	}

	got := mm.getCaptureByID(entry.CaptureID)
	if got == nil {
		t.Fatal("capture not found")
	}
	if string(got.ReqBody) != `{"model":"m","messages":[]}` {
		t.Errorf("req body = %q", got.ReqBody)
	}
	if len(got.RespBody) != 0 {
		t.Errorf("resp body stored for failed request (len=%d); want none", len(got.RespBody))
	}
	if got.RespHeaders["Content-Type"] != "application/json" {
		t.Errorf("resp Content-Type = %q", got.RespHeaders["Content-Type"])
	}
}

func TestMetricsMonitor_RecordFailedRequestStatusFallback(t *testing.T) {
	// Non-JSON error body: ErrorMsg falls back to the HTTP status text.
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 10, 5)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	copier.WriteHeader(http.StatusBadGateway)
	copier.Write([]byte("<html>upstream down</html>"))

	mm.record("m", hint.TaskChat, r, copier, captureAll, nil, nil)

	entries := metricsEntries(t, mm)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].ErrorMsg != "502 Bad Gateway" {
		t.Errorf("error_msg = %q, want status text", entries[0].ErrorMsg)
	}
}

func TestMetricsMonitor_RecordFailedRequestCaptureDisabled(t *testing.T) {
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 10, 0) // captures disabled
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	copier.WriteHeader(http.StatusInternalServerError)
	copier.Write([]byte(`{"error":"boom"}`))

	mm.record("m", hint.TaskChat, r, copier, captureAll, []byte("req"), nil)

	entries := metricsEntries(t, mm)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].HasCapture {
		t.Fatal("captures disabled, HasCapture should be false")
	}
	// ErrorMsg is independent of whether captures are enabled.
	if entries[0].ErrorMsg != "boom" {
		t.Errorf("error_msg = %q, want boom", entries[0].ErrorMsg)
	}
	if mm.getCaptureByID(entries[0].CaptureID) != nil {
		t.Fatal("no capture should be stored when disabled")
	}
}

func TestMetricsMonitor_RecordDecompressionFailureSetsErrorMsg(t *testing.T) {
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 10, 5)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	copier.Header().Set("Content-Encoding", "gzip")
	copier.WriteHeader(http.StatusOK)
	copier.Write([]byte("not-really-gzip"))

	mm.record("m", hint.TaskChat, r, copier, captureAll, []byte("req"), nil)

	entries := metricsEntries(t, mm)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].ErrorMsg == "" {
		t.Fatal("expected ErrorMsg for decompression failure")
	}
	// Raw bytes must not be stored when the body could not be decoded.
	if entries[0].HasCapture {
		t.Fatal("decompression failure should not store a capture")
	}
}

func TestMetricsMonitor_DecodeResponseBody(t *testing.T) {
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 10, 5)

	// No Content-Encoding: body returned unchanged.
	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	copier.Write([]byte("plain"))
	got, err := mm.decodeResponseBody(copier, "/p")
	if err != nil || string(got) != "plain" {
		t.Fatalf("plain body = %q, err = %v", got, err)
	}

	// Bogus gzip payload: returns an error and no body (no raw bytes kept).
	w2 := httptest.NewRecorder()
	copier2 := newBodyCopier(w2)
	copier2.Header().Set("Content-Encoding", "gzip")
	copier2.Write([]byte("not-really-gzip"))
	got, err = mm.decodeResponseBody(copier2, "/p")
	if err == nil {
		t.Fatal("expected decompression error")
	}
	if got != nil {
		t.Errorf("expected nil body on failure, got %q", got)
	}
}

func TestServer_ExtractErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"openai object", `{"error":{"message":"rate limited"}}`, "rate limited"},
		{"string error", `{"error":"bad request"}`, "bad request"},
		{"message field", `{"message":"nope"}`, "nope"},
		{"detail field", `{"detail":"oops"}`, "oops"},
		{"object error ignored", `{"error":{"code":42}}`, ""},
		{"no error", `{"usage":{}}`, ""},
		{"invalid json", `not-json`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractErrorMessage([]byte(tc.body)); got != tc.want {
				t.Errorf("extractErrorMessage = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestServer_MetricsMiddleware_UpstreamAudioCaptureSkipsRespBody verifies that
// an /upstream/<model>/v1/audio/speech request uses the path-specific capture
// mask (headers only) rather than falling back to captureAll.
func TestServer_MetricsMiddleware_UpstreamAudioCaptureSkipsRespBody(t *testing.T) {
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 100, 5)
	cfg := config.Config{Models: map[string]config.ModelConfig{"m1": {}}}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("BINARY-AUDIO-DATA"))
	})
	handler := CreateMetricsMiddleware(mm, cfg)(inner)

	req := httptest.NewRequest(http.MethodPost, "/upstream/m1/v1/audio/speech", strings.NewReader(`{"model":"m1"}`))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	entries := metricsEntries(t, mm)
	if len(entries) == 0 {
		t.Fatal("no metrics recorded")
	}
	last := entries[len(entries)-1]
	if !last.HasCapture {
		t.Fatal("expected capture to be stored")
	}
	cap := mm.getCaptureByID(last.CaptureID)
	if cap == nil {
		t.Fatal("capture not found")
	}
	if len(cap.RespBody) != 0 {
		t.Errorf("RespBody stored for /upstream audio route (len=%d); want path-specific mask to skip body", len(cap.RespBody))
	}
	if len(cap.RespHeaders) == 0 {
		t.Error("RespHeaders not stored; want captureRespHeaders mask")
	}
}

func TestMetricsMonitor_RecordStoresHint(t *testing.T) {
	mm := newTestMetricsMonitor(t, nil, 10, 0)
	ctx, h := hint.New(httptest.NewRequest(http.MethodPost, "/", nil).Context())
	h.Route = hint.RoutePeer
	h.PeerID = "cloud"
	h.Upstream = hint.VLLM
	r := httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil).WithContext(ctx)

	copier := newBodyCopier(httptest.NewRecorder())
	copier.Header().Set("Content-Type", "application/json")
	copier.WriteHeader(http.StatusOK)
	copier.Write([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":20},"metrics":{"time_to_first_token_ms":100,"mean_itl_ms":20}}`))

	mm.record("m", hint.TaskEmbedding, r, copier, 0, nil, nil)

	entries := metricsEntries(t, mm)
	require.Len(t, entries, 1)
	e := entries[0]
	assert.Equal(t, string(hint.TaskEmbedding), e.Task)
	assert.Equal(t, string(hint.RoutePeer), e.Route)
	assert.Equal(t, "cloud", e.PeerID)
	// The vLLM hint selects the vLLM extractor, which reads metrics only.
	assert.InDelta(t, 50.0, e.Tokens.TokensPerSecond, 1e-9)
	assert.Equal(t, -1, e.Tokens.ReasoningTokens)
}

func TestMetricsMonitor_RecordWithoutHint(t *testing.T) {
	mm := newTestMetricsMonitor(t, nil, 10, 0)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	copier := newBodyCopier(httptest.NewRecorder())
	copier.WriteHeader(http.StatusOK)
	copier.Write([]byte(`{"error":"boom"}`))

	mm.record("m", hint.TaskChat, r, copier, 0, nil, nil)

	entries := metricsEntries(t, mm)
	require.Len(t, entries, 1)
	assert.Equal(t, "chat", entries[0].Task)
	assert.Empty(t, entries[0].Route)
	assert.Empty(t, entries[0].PeerID)
}
