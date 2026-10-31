package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestServer_UsageVLLM_ChatCompletion(t *testing.T) {
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "vllm/chat_completion.json")
	assert.Equal(t, 42, u.Tokens.InputTokens)
	assert.Equal(t, 128, u.Tokens.OutputTokens)
	assert.Equal(t, 20, u.Tokens.CachedTokens)
	// The prompt rate counts only uncached tokens over time to first token.
	assert.InDelta(t, float64(42-20)/(85.2/1000), u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 1000/9.1, u.Tokens.TokensPerSecond, 1e-9, "1000 / mean_itl_ms")
}

func TestServer_UsageVLLM_ChatCompletionStream(t *testing.T) {
	// vLLM only emits the metrics object on the final streamed chunk.
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "vllm/chat_completion_stream.sse")
	assert.Equal(t, 14, u.Tokens.InputTokens)
	assert.Equal(t, 152, u.Tokens.OutputTokens)
	assert.Equal(t, -1, u.Tokens.CachedTokens)
	assert.InDelta(t, 200.0, u.Tokens.PromptPerSecond, 1e-9, "no cache count, so all 14 prompt tokens")
	assert.Equal(t, 100.0, u.Tokens.TokensPerSecond)
}

func TestServer_UsageVLLM_SpeculativeDecoding(t *testing.T) {
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "vllm/chat_completion_spec_decode_stream.sse")
	assert.Equal(t, 20, u.Tokens.DraftTokens)
	assert.Equal(t, 14, u.Tokens.DraftAccTokens)
}

func TestServer_UsageVLLM_ReasoningTokens(t *testing.T) {
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "vllm/chat_completion_reasoning_stream.sse")
	assert.Equal(t, 8, u.Tokens.OutputTokens)
	assert.Equal(t, 6, u.Tokens.ReasoningTokens)
}

func TestServer_UsageVLLM_SpeculativeDecodingNeedsBothCounters(t *testing.T) {
	// Half a speculative_decoding object must not produce an acceptance rate.
	u := parseUsageJSON(t, hint.VLLM, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":42,"completion_tokens":128},"metrics":{"speculative_decoding":{"num_draft_tokens":20}}}`)
	assert.Equal(t, -1, u.Tokens.DraftTokens)
	assert.Equal(t, -1, u.Tokens.DraftAccTokens)
}

func TestServer_UsageVLLM_IgnoresTimings(t *testing.T) {
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "llama-server/chat_completion.json")
	assert.Equal(t, 20, u.Tokens.InputTokens, "usage block is still read")
	assert.Equal(t, -1, u.Tokens.CachedTokens, "cache_n is llama-server's")
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
}
