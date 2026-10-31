package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestServer_UsageVLLM_Metrics(t *testing.T) {
	u := parseUsageJSON(t, hint.VLLM, "/v1/chat/completions",
		`{"id":"chatcmpl-abc123","object":"chat.completion","usage":{"prompt_tokens":42,"completion_tokens":128,"total_tokens":170,"prompt_tokens_details":{"cached_tokens":20}},"metrics":{"time_to_first_token_ms":85.2,"generation_time_ms":1240.5,"queue_time_ms":12.3,"mean_itl_ms":9.1,"tokens_per_second":103.2}}`)
	assert.Equal(t, 42, u.Tokens.InputTokens)
	assert.Equal(t, 128, u.Tokens.OutputTokens)
	assert.Equal(t, 20, u.Tokens.CachedTokens)
	// The prompt rate counts only uncached tokens.
	assert.InDelta(t, float64(42-20)/(85.2/1000), u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 1000/9.1, u.Tokens.TokensPerSecond, 1e-9)
}

func TestServer_UsageVLLM_MetricsStream(t *testing.T) {
	// vLLM only emits the metrics object on the final streamed chunk.
	u := parseUsageStream(t, hint.VLLM, "/v1/chat/completions",
		contentChunk,
		`{"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":14,"total_tokens":166,"completion_tokens":152},"metrics":{"time_to_first_token_ms":70,"mean_itl_ms":10,"tokens_per_second":24.116032676555495}}`)
	assert.Equal(t, 14, u.Tokens.InputTokens)
	assert.Equal(t, 152, u.Tokens.OutputTokens)
	assert.Equal(t, -1, u.Tokens.CachedTokens)
	assert.InDelta(t, 200.0, u.Tokens.PromptPerSecond, 1e-9, "no cache count, so all 14 prompt tokens")
	assert.Equal(t, 100.0, u.Tokens.TokensPerSecond)
}

func TestServer_UsageVLLM_SpeculativeDecoding(t *testing.T) {
	u := parseUsageStream(t, hint.VLLM, "/v1/chat/completions",
		contentChunk,
		`{"choices":[],"usage":{"prompt_tokens":11742,"completion_tokens":35},"metrics":{"mean_itl_ms":17.2,"speculative_decoding":{"mean_acceptance_length":1.7,"draft_acceptance_rate":0.7,"num_spec_steps":20,"num_accepted_draft_tokens":14,"num_draft_tokens":20,"num_spec_tokens":1}}}`)
	assert.Equal(t, 20, u.Tokens.DraftTokens)
	assert.Equal(t, 14, u.Tokens.DraftAccTokens)
}

func TestServer_UsageVLLM_SpeculativeDecodingNeedsBothCounters(t *testing.T) {
	// Half a speculative_decoding object must not produce an acceptance rate.
	u := parseUsageJSON(t, hint.VLLM, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":42,"completion_tokens":128},"metrics":{"speculative_decoding":{"num_draft_tokens":20}}}`)
	assert.Equal(t, -1, u.Tokens.DraftTokens)
	assert.Equal(t, -1, u.Tokens.DraftAccTokens)
}

func TestServer_UsageVLLM_ReasoningTokens(t *testing.T) {
	u := parseUsageStream(t, hint.VLLM, "/v1/chat/completions",
		contentChunk,
		`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":8,"completion_tokens_details":{"reasoning_tokens":6}}}`)
	assert.Equal(t, 6, u.Tokens.ReasoningTokens)
	assert.Equal(t, 8, u.Tokens.OutputTokens)
}

func TestServer_UsageVLLM_IgnoresTimings(t *testing.T) {
	u := parseUsageJSON(t, hint.VLLM, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":10,"completion_tokens":20},"timings":{"prompt_n":99,"predicted_n":99,"predicted_per_second":5}}`)
	assert.Equal(t, 10, u.Tokens.InputTokens)
	assert.Equal(t, 20, u.Tokens.OutputTokens)
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
}
