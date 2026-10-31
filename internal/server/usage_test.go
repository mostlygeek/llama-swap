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
		{hint.Anthropic, anthropicExtractor{}},
		{hint.OpenAI, openaiExtractor{}},
		{hint.OpenRouter, openaiExtractor{}},
		{hint.OpenAICompatible, openaiExtractor{}},
		{hint.Halogen, openaiExtractor{}},
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
