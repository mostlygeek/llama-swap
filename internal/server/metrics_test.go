package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/tidwall/gjson"
)

func TestServer_ParseMetrics_ChatCompletions(t *testing.T) {
	body := `{"usage":{"prompt_tokens":12,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}}`
	parsed := gjson.Parse(body)
	entry, err := parseMetrics("m", time.Now(), parsed.Get("usage"), parsed.Get("timings"))
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
	entry, err := parseMetrics("m", time.Now(), parsed.Get("usage"), parsed.Get("timings"))
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

func TestServer_ProcessStreamingResponse_NoData(t *testing.T) {
	if _, err := processStreamingResponse("m", time.Now(), []byte("data: [DONE]\n\n")); err == nil {
		t.Fatal("expected error for stream with no usage data")
	}
}

func TestMetricsMonitor_RecordMetadata(t *testing.T) {
	mm := newMetricsMonitor(nil, 10, 0)
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

	entries := mm.getMetrics()
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

func TestMetricsMonitor_RecordMetadata_EmptyMap(t *testing.T) {
	// An empty Metadata map in context must NOT set tm.Metadata (omitempty semantics).
	mm := newMetricsMonitor(nil, 10, 0)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	r = r.WithContext(shared.SetContext(r.Context(), shared.ReqContextData{
		ModelID:  "m",
		Metadata: map[string]string{}, // empty, not nil
	}))

	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	copier.WriteHeader(http.StatusOK)
	copier.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`))

	mm.record("m", r, copier, 0, nil, nil)

	entries := mm.getMetrics()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Metadata != nil {
		t.Errorf("Metadata should be nil for empty context metadata, got %v", entries[0].Metadata)
	}
}

func TestMetricsMonitor_RecordMetadata_NoContextData(t *testing.T) {
	// A request with no ReqContextData in context should produce nil Metadata.
	mm := newMetricsMonitor(nil, 10, 0)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	// No shared.SetContext call — no ReqContextData in context.

	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	copier.WriteHeader(http.StatusOK)
	copier.Write([]byte(`{"usage":{"prompt_tokens":3,"completion_tokens":4}}`))

	mm.record("m", r, copier, 0, nil, nil)

	entries := mm.getMetrics()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Metadata != nil {
		t.Errorf("Metadata should be nil when no context data, got %v", entries[0].Metadata)
	}
}

func TestMetricsMonitor_RecordMetadata_DeepCopy(t *testing.T) {
	// Mutating the original context metadata after record() must not affect the stored entry.
	mm := newMetricsMonitor(nil, 10, 0)
	original := map[string]string{"key": "before"}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	r = r.WithContext(shared.SetContext(r.Context(), shared.ReqContextData{
		ModelID:  "m",
		Metadata: original,
	}))

	w := httptest.NewRecorder()
	copier := newBodyCopier(w)
	copier.WriteHeader(http.StatusOK)
	copier.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`))

	mm.record("m", r, copier, 0, nil, nil)

	// Mutate the original map after record.
	original["key"] = "after"

	entries := mm.getMetrics()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Metadata["key"] != "before" {
		t.Errorf("Metadata[key] = %q, want %q (deep copy expected)", entries[0].Metadata["key"], "before")
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
	entry, err := parseMetrics("m", time.Now(), parsed.Get("usage"), timings)
	if err != nil {
		t.Fatalf("parseMetrics: %v", err)
	}
	if entry.Tokens.InputTokens != 5 || entry.Tokens.OutputTokens != 9 {
		t.Fatalf("tokens = %+v", entry.Tokens)
	}
}
