package proxy

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTelemetryManager_RecordActivity(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() {
		require.NoError(t, tp.Shutdown(context.Background()))
	})

	tm := &TelemetryManager{
		tp:              tp,
		tracer:          tp.Tracer("test"),
		serviceName:     "llama-swap",
		environment:     "local",
		captureInput:    true,
		captureOutput:   true,
		maxContentBytes: 4096,
		enabled:         true,
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), proxyCtxKey("streaming"), true))

	metric := ActivityLogEntry{
		ID:              7,
		Timestamp:       time.Date(2026, 5, 6, 4, 11, 57, 808000000, time.UTC),
		Model:           "test-model",
		ReqPath:         req.URL.Path,
		RespContentType: "application/json",
		RespStatusCode:  200,
		Tokens: TokenMetrics{
			InputTokens:  3,
			OutputTokens: 5,
			CachedTokens: 1,
		},
		DurationMs: 123,
		HasCapture: true,
	}

	reqBody := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	respBody := []byte(`{"model":"test-model","choices":[{"message":{"content":"hi"}}]}`)
	tm.RecordActivity(req.Context(), metric, req, reqBody, respBody)

	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	span := spans[0]

	assert.Equal(t, "llama-swap /v1/chat/completions", span.Name)
	assert.Equal(t, codes.Unset, span.Status.Code)
	assert.Equal(t, "generation", attrString(span.Attributes, "langfuse.observation.type"))
	assert.Equal(t, "chat", attrString(span.Attributes, "gen_ai.operation.name"))
	assert.Equal(t, "test-model", attrString(span.Attributes, "gen_ai.request.model"))
	assert.Equal(t, "test-model", attrString(span.Attributes, "gen_ai.response.model"))
	assert.Equal(t, "test-model", attrString(span.Attributes, "langfuse.observation.model.name"))
	assert.Equal(t, "true", attrString(span.Attributes, "langfuse.observation.metadata.streaming"))
	assert.Contains(t, attrString(span.Attributes, "gen_ai.prompt"), "hello")
	assert.Equal(t, "hi", attrString(span.Attributes, "gen_ai.completion"))
	assert.Equal(t, "local", attrString(span.Attributes, "langfuse.environment"))
	assert.Equal(t, int64(7), attrInt(span.Attributes, "langfuse.observation.metadata.activity_id"))
	assert.Equal(t, 123*time.Millisecond, span.EndTime.Sub(span.StartTime))
}

func TestTelemetryManager_UsesResponseModelFromStream(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() {
		require.NoError(t, tp.Shutdown(context.Background()))
	})

	tm := &TelemetryManager{
		tp:              tp,
		tracer:          tp.Tracer("test"),
		captureOutput:   true,
		maxContentBytes: 4096,
		enabled:         true,
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")

	metric := ActivityLogEntry{
		ID:              8,
		Timestamp:       time.Date(2026, 5, 6, 4, 11, 57, 808000000, time.UTC),
		Model:           "request-model",
		ReqPath:         req.URL.Path,
		RespContentType: "text/event-stream",
		RespStatusCode:  200,
		DurationMs:      10,
	}

	respBody := []byte(`data: {"choices":[{"finish_reason":null,"index":0,"delta":{"role":"assistant","content":null}}],"created":1778040349,"id":"chatcmpl-yQMMAFzny5PM9OKAjg4lXbnwLSL5O2f1","model":"gemma-4-E2B-it-Q4_K_M.gguf","system_fingerprint":"b9020-a4701c98f","object":"chat.completion.chunk"}
data: {"choices":[{"finish_reason":"stop","index":0,"delta":{}}],"created":1778040351,"id":"chatcmpl-yQMMAFzny5PM9OKAjg4lXbnwLSL5O2f1","model":"gemma-4-E2B-it-Q4_K_M.gguf","system_fingerprint":"b9020-a4701c98f","object":"chat.completion.chunk","timings":{"cache_n":0,"prompt_n":20,"prompt_ms":148.2,"prompt_per_token_ms":7.409999999999999,"prompt_per_second":134.9527665317139,"predicted_n":81,"predicted_ms":2025.561,"predicted_per_token_ms":25.006925925925923,"predicted_per_second":39.98892158764905}}
data: [DONE]
`)

	tm.RecordActivity(req.Context(), metric, req, nil, respBody)

	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	span := spans[0]

	assert.Equal(t, "llama-swap /v1/chat/completions", span.Name)
	assert.Equal(t, "request-model", attrString(span.Attributes, "gen_ai.request.model"))
	assert.Equal(t, "gemma-4-E2B-it-Q4_K_M.gguf", attrString(span.Attributes, "gen_ai.response.model"))
	assert.Equal(t, "gemma-4-E2B-it-Q4_K_M.gguf", attrString(span.Attributes, "langfuse.observation.model.name"))
	assert.Equal(t, "2", attrString(span.Attributes, "gen_ai.completion"))
	assert.Equal(t, 10*time.Millisecond, span.EndTime.Sub(span.StartTime))
}

func TestTelemetryManager_SkipsBinaryOutput(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() {
		require.NoError(t, tp.Shutdown(context.Background()))
	})

	tm := &TelemetryManager{
		tp:              tp,
		tracer:          tp.Tracer("test"),
		captureOutput:   true,
		maxContentBytes: 1024,
		enabled:         true,
	}

	req := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"prompt":"draw a cat"}`))
	req.Header.Set("Content-Type", "application/json")

	metric := ActivityLogEntry{
		ID:              1,
		Model:           "image-model",
		ReqPath:         req.URL.Path,
		RespContentType: "image/png",
		RespStatusCode:  200,
		DurationMs:      50,
	}

	tm.RecordActivity(req.Context(), metric, req, []byte(`{"prompt":"draw a cat"}`), []byte{0x89, 0x50, 0x4e, 0x47})

	require.NoError(t, tp.ForceFlush(context.Background()))
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Empty(t, attrString(spans[0].Attributes, "gen_ai.completion"))
}

func attrString(attrs []attribute.KeyValue, key string) string {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}

func attrInt(attrs []attribute.KeyValue, key string) int64 {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsInt64()
		}
	}
	return 0
}
