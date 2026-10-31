package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// usageBothBlocks reports speeds through both llama-server timings and vLLM
// metrics, with different values, so a test can tell which block was read.
const usageBothBlocks = `{"usage":{"prompt_tokens":10,"completion_tokens":20},` +
	`"timings":{"prompt_n":10,"predicted_n":20,"prompt_per_second":111,"predicted_per_second":222,"prompt_ms":90,"predicted_ms":90},` +
	`"metrics":{"time_to_first_token_ms":100,"mean_itl_ms":20}}`

func TestServer_ExtractorFor(t *testing.T) {
	tests := []struct {
		upstream hint.Upstream
		want     usageExtractor
	}{
		{hint.LlamaServer, llamaServerExtractor{}},
		{hint.VLLM, vllmExtractor{}},
		{hint.TabbyAPI, tabbyAPIExtractor{}},
		{hint.OMLX, omlxExtractor{}},
		{hint.Anthropic, apiExtractor{}},
		{hint.OpenAI, apiExtractor{}},
		{hint.OpenRouter, apiExtractor{}},
		{hint.OpenAICompatible, apiExtractor{}},
		{hint.Halogen, apiExtractor{}},
		{hint.LlamaSwap, autoExtractor{}},
		{hint.Unknown, autoExtractor{}},
	}
	for _, tt := range tests {
		assert.IsType(t, tt.want, extractorFor(tt.upstream), "upstream %q", tt.upstream)
	}
}

func TestServer_ExtractUsage_PerUpstreamBlocks(t *testing.T) {
	tests := []struct {
		upstream   hint.Upstream
		wantPrompt float64
		wantGen    float64
	}{
		// llama-server reads timings and ignores metrics.
		{hint.LlamaServer, 111, 222},
		// vLLM reads metrics and ignores timings.
		{hint.VLLM, 100, 50},
		// OpenAI reads neither; speeds stay unreported.
		{hint.OpenAI, -1, -1},
		// auto reads both; metrics are read after timings and win.
		{hint.Unknown, 100, 50},
	}
	for _, tt := range tests {
		t.Run(string(tt.upstream), func(t *testing.T) {
			u, err := extractUsage(tt.upstream, "/v1/chat/completions", "application/json", []byte(usageBothBlocks), time.Now())
			require.NoError(t, err)
			assert.True(t, u.Found)
			assert.Equal(t, 10, u.Tokens.InputTokens)
			assert.Equal(t, 20, u.Tokens.OutputTokens)
			assert.InDelta(t, tt.wantPrompt, u.Tokens.PromptPerSecond, 1e-9, "prompt speed")
			assert.InDelta(t, tt.wantGen, u.Tokens.TokensPerSecond, 1e-9, "gen speed")
		})
	}
}

func TestServer_ExtractUsage_ReasoningTokens(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"chat completions", `{"usage":{"prompt_tokens":5,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":7}}}`, 7},
		{"responses api", `{"response":{"usage":{"input_tokens":5,"output_tokens":9,"output_tokens_details":{"reasoning_tokens":4}}}}`, 4},
		{"zero is reported", `{"usage":{"prompt_tokens":5,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":0}}}`, 0},
		{"not reported", `{"usage":{"prompt_tokens":5,"completion_tokens":9}}`, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := extractUsage(hint.OpenAI, "/v1/chat/completions", "application/json", []byte(tt.body), time.Now())
			require.NoError(t, err)
			assert.Equal(t, tt.want, u.Tokens.ReasoningTokens)
		})
	}
}

func TestServer_ExtractUsage_ReasoningTokensStream(t *testing.T) {
	body := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":8,\"completion_tokens_details\":{\"reasoning_tokens\":6}}}\n\n" +
		"data: [DONE]\n\n")
	u, err := extractUsage(hint.VLLM, "/v1/chat/completions", "text/event-stream", body, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 6, u.Tokens.ReasoningTokens)
	assert.Equal(t, 8, u.Tokens.OutputTokens)
}

func TestServer_MeteredTask(t *testing.T) {
	tests := []struct {
		path    string
		want    hint.Task
		metered bool
	}{
		{"/v1/chat/completions", hint.TaskChat, true},
		{"/v1/completions", hint.TaskChat, true},
		{"/v1/messages", hint.TaskChat, true},
		{"/infill", hint.TaskChat, true},
		{"/v/chat/completions", hint.TaskChat, true},
		{"/v1/embeddings", hint.TaskEmbedding, true},
		{"/v1/rerank", hint.TaskRerank, true},
		{"/v1/messages/count_tokens", hint.TaskTokenCount, true},
		{"/v1/images/generations", hint.TaskImageGeneration, true},
		{"/sdapi/v1/img2img", hint.TaskImageEdit, true},
		{"/v1/audio/speech", hint.TaskSpeech, true},
		{"/v1/audio/transcriptions", hint.TaskTranscription, true},
		{"/v1/systemone", hint.TaskDecision, true},
		{"/audioapi/v1/tasks/run", hint.TaskAudio, true},
		// listing endpoints are not inference
		{"/v1/audio/voices", "", false},
		{"/v1/models", "", false},
		{"/props", "", false},
	}
	for _, tt := range tests {
		task, metered := meteredTask(tt.path)
		assert.Equal(t, tt.metered, metered, "metered %s", tt.path)
		assert.Equal(t, tt.want, task, "task %s", tt.path)
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
	copier.Write([]byte(usageBothBlocks))

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

func TestServer_ExtractUsage_AnthropicStream(t *testing.T) {
	body := []byte(`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":25,"cache_read_input_tokens":10,"output_tokens":1}}}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}

event: message_delta
data: {"type":"message_delta","usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}
`)
	for _, upstream := range []hint.Upstream{hint.Anthropic, hint.LlamaServer, hint.VLLM, hint.Unknown} {
		t.Run(string(upstream), func(t *testing.T) {
			u, err := extractUsage(upstream, "/v1/messages", "text/event-stream", body, time.Now())
			require.NoError(t, err)
			assert.Equal(t, 25, u.Tokens.InputTokens)
			assert.Equal(t, 42, u.Tokens.OutputTokens)
			assert.Equal(t, 10, u.Tokens.CachedTokens)
			assert.Equal(t, -1, u.Tokens.ReasoningTokens, "anthropic does not report reasoning separately")
		})
	}
}

func TestServer_ExtractUsage_AnthropicJSON(t *testing.T) {
	body := `{"type":"message","usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":0}}`
	u, err := extractUsage(hint.Anthropic, "/v1/messages", "application/json", []byte(body), time.Now())
	require.NoError(t, err)
	assert.Equal(t, 7, u.Tokens.InputTokens)
	assert.Equal(t, 3, u.Tokens.OutputTokens)
	assert.Equal(t, 0, u.Tokens.CachedTokens)
}

func TestServer_ExtractUsage_ResponsesStream(t *testing.T) {
	body := []byte(`data: {"type":"response.output_text.delta","delta":"hi"}

data: {"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":5,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}}
`)
	u, err := extractUsage(hint.OpenAI, "/v1/responses", "text/event-stream", body, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 11, u.Tokens.InputTokens)
	assert.Equal(t, 5, u.Tokens.OutputTokens)
	assert.Equal(t, 3, u.Tokens.CachedTokens)
	assert.Equal(t, 2, u.Tokens.ReasoningTokens)
}

func TestServer_ExtractUsage_APIExtractorIgnoresServerFields(t *testing.T) {
	body := `{"usage":{"prompt_tokens":4,"completion_tokens":6,"prompt_tokens_per_sec":9,"total_time":99},` +
		`"timings":{"prompt_n":40,"predicted_n":60,"predicted_per_second":5}}`
	start := time.Now()
	u, err := extractUsage(hint.OpenAI, "/v1/chat/completions", "application/json", []byte(body), start)
	require.NoError(t, err)
	assert.Equal(t, 4, u.Tokens.InputTokens, "timings are llama-server's, not OpenAI's")
	assert.Equal(t, 6, u.Tokens.OutputTokens)
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond, "rates in usage are TabbyAPI's")
	assert.Less(t, u.DurationMs, 99000, "total_time is TabbyAPI's")
}

func TestServer_ExtractUsage_TabbyAPI(t *testing.T) {
	body := `{"usage":{"prompt_tokens":4,"completion_tokens":6,"prompt_tokens_per_sec":9,"completion_tokens_per_sec":3,"total_time":2.5},` +
		`"timings":{"prompt_n":40,"predicted_n":60,"predicted_per_second":5}}`
	u, err := extractUsage(hint.TabbyAPI, "/v1/chat/completions", "application/json", []byte(body), time.Now())
	require.NoError(t, err)
	assert.Equal(t, 4, u.Tokens.InputTokens, "TabbyAPI ignores timings")
	assert.Equal(t, 9.0, u.Tokens.PromptPerSecond)
	assert.Equal(t, 3.0, u.Tokens.TokensPerSecond)
	assert.Equal(t, 2500, u.DurationMs)
}

func TestServer_ExtractUsage_OMLX(t *testing.T) {
	body := `{"usage":{"prompt_tokens":4,"completion_tokens":6,"prompt_tokens_per_second":12,"generation_tokens_per_second":8,"prompt_tokens_per_sec":99}}`
	u, err := extractUsage(hint.OMLX, "/v1/chat/completions", "application/json", []byte(body), time.Now())
	require.NoError(t, err)
	assert.Equal(t, 12.0, u.Tokens.PromptPerSecond, "oMLX ignores TabbyAPI's field names")
	assert.Equal(t, 8.0, u.Tokens.TokensPerSecond)
}

func TestServer_ExtractUsage_LlamaServerInfill(t *testing.T) {
	body := `[{"content":"a"},{"content":"b","timings":{"prompt_n":9,"predicted_n":4,"prompt_per_second":30,"predicted_per_second":20}}]`
	u, err := extractUsage(hint.LlamaServer, "/infill", "application/json", []byte(body), time.Now())
	require.NoError(t, err)
	assert.True(t, u.Found)
	assert.Equal(t, 9, u.Tokens.InputTokens)
	assert.Equal(t, 20.0, u.Tokens.TokensPerSecond)
}
