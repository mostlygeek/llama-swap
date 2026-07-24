package server

import (
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/tidwall/gjson"
)

func TestServer_ParseMetrics_ChatCompletions(t *testing.T) {
	body := `{"usage":{"prompt_tokens":12,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}}`
	parsed := gjson.Parse(body)
	entry, err := parseMetrics("m", time.Now(), parsed.Get("usage"), parsed.Get("timings"), parsed.Get("metrics"))
	if err != nil {
		t.Fatalf("parseMetrics: %v", err)
	}
	if entry.Tokens.InputTokens != 12 || entry.Tokens.OutputTokens != 7 || entry.Tokens.CachedTokens != 4 {
		t.Fatalf("tokens = %+v", entry.Tokens)
	}
}

func TestServer_ParseMetrics_Timings(t *testing.T) {
	body := `{"timings":{"prompt_n":20,"predicted_n":50,"prompt_per_second":100.0,"predicted_per_second":40.0,"prompt_ms":200,"predicted_ms":1250,"cache_n":8}}`
	parsed := gjson.Parse(body)
	entry, err := parseMetrics("m", time.Now(), parsed.Get("usage"), parsed.Get("timings"), parsed.Get("metrics"))
	if err != nil {
		t.Fatalf("parseMetrics: %v", err)
	}
	if entry.Tokens.InputTokens != 20 || entry.Tokens.OutputTokens != 50 || entry.Tokens.CachedTokens != 8 {
		t.Fatalf("tokens = %+v", entry.Tokens)
	}
	if entry.Tokens.TokensPerSecond != 40.0 || entry.Tokens.PromptPerSecond != 100.0 {
		t.Fatalf("rates = %+v", entry.Tokens)
	}
	if entry.DurationMs != 1450 {
		t.Fatalf("DurationMs = %d, want 1450", entry.DurationMs)
	}
}

func TestServer_ProcessStreamingResponse(t *testing.T) {
	body := []byte("data: {\"choices\":[{}]}\n\n" +
		"data: {\"usage\":{\"prompt_tokens\":15,\"completion_tokens\":33}}\n\n" +
		"data: [DONE]\n\n")
	entry, err := processStreamingResponse("m", time.Now(), body)
	if err != nil {
		t.Fatalf("processStreamingResponse: %v", err)
	}
	if entry.Tokens.InputTokens != 15 || entry.Tokens.OutputTokens != 33 {
		t.Fatalf("tokens = %+v", entry.Tokens)
	}
}

func TestServer_ProcessStreamingResponse_VLLMMetrics(t *testing.T) {
	body := []byte(`data: {"id":"chatcmpl-b7a832cea986aea4","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":14,"total_tokens":166,"completion_tokens":152},"metrics":{"time_to_first_token_ms":70,"mean_itl_ms":10,"tokens_per_second":24.116032676555495}}

data: [DONE]
`)
	entry, err := processStreamingResponse("m", time.Now(), body)
	if err != nil {
		t.Fatalf("processStreamingResponse: %v", err)
	}
	if entry.Tokens.InputTokens != 14 || entry.Tokens.OutputTokens != 152 {
		t.Fatalf("tokens = %+v", entry.Tokens)
	}
	if entry.Tokens.CachedTokens != -1 {
		t.Errorf("CachedTokens = %d, want -1", entry.Tokens.CachedTokens)
	}
	if got, want := entry.Tokens.PromptPerSecond, 200.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("PromptPerSecond = %v, want %v", got, want)
	}
	if entry.Tokens.TokensPerSecond != 100 {
		t.Errorf("TokensPerSecond = %v, want 100", entry.Tokens.TokensPerSecond)
	}
}

func TestServer_ParseMetrics_VLLMMetrics(t *testing.T) {
	body := `{"id":"chatcmpl-abc123","object":"chat.completion","usage":{"prompt_tokens":42,"completion_tokens":128,"total_tokens":170,"prompt_tokens_details":{"cached_tokens":20}},"metrics":{"time_to_first_token_ms":85.2,"generation_time_ms":1240.5,"queue_time_ms":12.3,"mean_itl_ms":9.1,"tokens_per_second":103.2}}`
	parsed := gjson.Parse(body)
	entry, err := parseMetrics("m", time.Now(), parsed.Get("usage"), parsed.Get("timings"), parsed.Get("metrics"))
	if err != nil {
		t.Fatalf("parseMetrics: %v", err)
	}
	if entry.Tokens.InputTokens != 42 || entry.Tokens.OutputTokens != 128 || entry.Tokens.CachedTokens != 20 {
		t.Fatalf("tokens = %+v", entry.Tokens)
	}
	if got, want := entry.Tokens.PromptPerSecond, float64(42-20)/(85.2/1000); math.Abs(got-want) > 1e-9 {
		t.Errorf("PromptPerSecond = %v, want %v", got, want)
	}
	if got, want := entry.Tokens.TokensPerSecond, 1000/9.1; math.Abs(got-want) > 1e-9 {
		t.Errorf("TokensPerSecond = %v, want %v", got, want)
	}
}

func TestServer_ProcessStreamingResponse_NoData(t *testing.T) {
	if _, err := processStreamingResponse("m", time.Now(), []byte("data: [DONE]\n\n")); err == nil {
		t.Fatal("expected error for stream with no usage data")
	}
}

func TestMetricsMonitor_RecordMetadata(t *testing.T) {
	mm := newTestMetricsMonitor(t, nil, 10, 0)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"usage":{}}`))
	r = r.WithContext(shared.SetContext(r.Context(), shared.ReqContextData{
		ModelID:  "m",
		Metadata: map[string]string{"client": "web", "trace": "abc"},
	}))

	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	copier.WriteHeader(http.StatusOK)
	copier.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`))

	mm.record("m", r, copier, 0, nil, nil)

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
	mm.record("m", r, copier, captureAll, reqBody, reqHeaders)

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

	got := mm.getCaptureByID(entry.ID)
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

	mm.record("m", r, copier, captureAll, nil, nil)

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

	mm.record("m", r, copier, captureAll, []byte("req"), nil)

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
	if mm.getCaptureByID(entries[0].ID) != nil {
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

	mm.record("m", r, copier, captureAll, []byte("req"), nil)

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

func TestServer_ParseMetrics_Infill(t *testing.T) {
	// /infill responses are arrays; timings live in the last element.
	body := `[{"content":"a"},{"content":"b","timings":{"prompt_n":5,"predicted_n":9,"prompt_ms":10,"predicted_ms":20}}]`
	parsed := gjson.Parse(body)
	timings := parsed.Get("timings")
	if arr := parsed.Array(); len(arr) > 0 {
		timings = arr[len(arr)-1].Get("timings")
	}
	entry, err := parseMetrics("m", time.Now(), parsed.Get("usage"), timings, parsed.Get("metrics"))
	if err != nil {
		t.Fatalf("parseMetrics: %v", err)
	}
	if entry.Tokens.InputTokens != 5 || entry.Tokens.OutputTokens != 9 {
		t.Fatalf("tokens = %+v", entry.Tokens)
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
	cap := mm.getCaptureByID(last.ID)
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
