package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/logmon"
)

// TestServer_Record_VLLMStreamingNoUsage_UsesUnknownSentinel covers the vLLM
// case: a streaming response that omits usage chunks (the client did not send
// stream_options.include_usage). The recorded entry must keep the -1 "unknown"
// sentinel for rates and cache rather than reporting a misleading 0 t/s, while
// token counts stay at 0.
func TestServer_Record_VLLMStreamingNoUsage_UsesUnknownSentinel(t *testing.T) {
	mp := newMetricsMonitor(logmon.NewWriter(io.Discard), 10, 0, "", 0)

	rec := httptest.NewRecorder()
	copier := newBodyCopier(rec)
	copier.Header().Set("Content-Type", "text/event-stream")
	copier.WriteHeader(http.StatusOK)
	// Content deltas only, no usage/timings chunk — processStreamingResponse
	// finds nothing to measure and the default sentinels survive.
	_, _ = copier.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	mp.record("test-model", req, copier, captureFields(0), nil, nil)

	got := mp.getMetrics()
	if len(got) != 1 {
		t.Fatalf("recorded %d metrics, want 1", len(got))
	}
	tk := got[0].Tokens
	if tk.PromptPerSecond != -1 || tk.TokensPerSecond != -1 || tk.CachedTokens != -1 {
		t.Errorf("want -1 unknown sentinels, got prompt/s=%v tokens/s=%v cache=%d",
			tk.PromptPerSecond, tk.TokensPerSecond, tk.CachedTokens)
	}
	if tk.InputTokens != 0 || tk.OutputTokens != 0 {
		t.Errorf("token counts should be 0, got in=%d out=%d", tk.InputTokens, tk.OutputTokens)
	}
}

// TestServer_Record_NonOKResponse_UsesUnknownSentinel verifies the failure path
// also reports unknown rates rather than 0.
func TestServer_Record_NonOKResponse_UsesUnknownSentinel(t *testing.T) {
	mp := newMetricsMonitor(logmon.NewWriter(io.Discard), 10, 0, "", 0)

	rec := httptest.NewRecorder()
	copier := newBodyCopier(rec)
	copier.WriteHeader(http.StatusInternalServerError)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	mp.record("test-model", req, copier, captureFields(0), nil, nil)

	got := mp.getMetrics()
	if len(got) != 1 {
		t.Fatalf("recorded %d metrics, want 1", len(got))
	}
	tk := got[0].Tokens
	if tk.PromptPerSecond != -1 || tk.TokensPerSecond != -1 || tk.CachedTokens != -1 {
		t.Errorf("want -1 unknown sentinels on failure path, got prompt/s=%v tokens/s=%v cache=%d",
			tk.PromptPerSecond, tk.TokensPerSecond, tk.CachedTokens)
	}
}
