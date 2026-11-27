package usage

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestUsage_VLLM_ChatCompletion(t *testing.T) {
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "vllm/chat_completion.json")
	assert.Equal(t, 42, u.Tokens.InputTokens)
	assert.Equal(t, 128, u.Tokens.OutputTokens)
	assert.Equal(t, 20, u.Tokens.CachedTokens)
	// The prompt rate counts only uncached tokens over the prefill time:
	// time to first token minus time spent queued.
	assert.InDelta(t, float64(42-20)/((85.2-12.3)/1000), u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 1000/9.1, u.Tokens.TokensPerSecond, 1e-9, "1000 / mean_itl_ms")
}

func TestUsage_VLLM_ChatCompletionStream(t *testing.T) {
	// Captured from vLLM 0.29.1; the metrics object is only on the final
	// streamed chunk, next to usage.
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "vllm/chat_completion_stream.sse")
	assert.True(t, u.Found)
	assert.Equal(t, 42, u.Tokens.InputTokens)
	assert.Equal(t, 227, u.Tokens.OutputTokens)
	assert.Equal(t, 0, u.Tokens.CachedTokens, "reported, nothing cached")
	assert.InDelta(t, 42/((136.54924900038168-0.021259002096485347)/1000), u.Tokens.PromptPerSecond, 1e-9, "all 42 prompt tokens over prefill time")
	assert.InDelta(t, 1000/5.561512570794202, u.Tokens.TokensPerSecond, 1e-9, "1000 / mean_itl_ms")
	assert.Equal(t, -1, u.Tokens.DraftTokens)
}

func TestUsage_VLLM_SpeculativeDecoding(t *testing.T) {
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "vllm/chat_completion_spec_decode_stream.sse")
	assert.Equal(t, 20, u.Tokens.DraftTokens)
	assert.Equal(t, 14, u.Tokens.DraftAccTokens)
}

func TestUsage_VLLM_ReasoningTokens(t *testing.T) {
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "vllm/chat_completion_reasoning_stream.sse")
	assert.Equal(t, 8, u.Tokens.OutputTokens)
	assert.Equal(t, 6, u.Tokens.ReasoningTokens)
}

func TestUsage_VLLM_SpeculativeDecodingNeedsBothCounters(t *testing.T) {
	// Half a speculative_decoding object must not produce an acceptance rate.
	u := parseUsageJSON(t, hint.VLLM, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":42,"completion_tokens":128},"metrics":{"speculative_decoding":{"num_draft_tokens":20}}}`)
	assert.Equal(t, -1, u.Tokens.DraftTokens)
	assert.Equal(t, -1, u.Tokens.DraftAccTokens)
}

func TestUsage_VLLM_OneCatTimingsStream(t *testing.T) {
	// Captured from 1Cat-vLLM (a vLLM fork); it has no top-level metrics
	// object, only usage.timings with phase durations in seconds.
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "vllm/chat_completion_1cat_stream.sse")
	assert.True(t, u.Found)
	assert.Equal(t, 42, u.Tokens.InputTokens)
	assert.Equal(t, 96, u.Tokens.OutputTokens)
	assert.Equal(t, 42, u.Tokens.CachedTokens)
	// Fully cached prompt: nothing to prefill from scratch.
	assert.InDelta(t, 0, u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 96/8.79, u.Tokens.TokensPerSecond, 1e-9, "output tokens over decode_s")
}

func TestUsage_VLLM_OneCatTimingsNonStream(t *testing.T) {
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "vllm/chat_completion_1cat.json")
	assert.Equal(t, 42, u.Tokens.InputTokens)
	assert.Equal(t, 16, u.Tokens.OutputTokens)
	assert.InDelta(t, 0, u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 16/0.036, u.Tokens.TokensPerSecond, 1e-9)
}

func TestUsage_VLLM_MetricsBeatOneCatTimings(t *testing.T) {
	// A vLLM top-level metrics object is more precise and keeps precedence
	// over the 1Cat-vLLM usage.timings fallback.
	u := parseUsageJSON(t, hint.VLLM, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":42,"completion_tokens":96,`+
			`"prompt_tokens_details":{"cached_tokens":42},`+
			`"timings":{"prefill_s":0.193,"decode_s":8.79}},`+
			`"metrics":{"time_to_first_token_ms":136.5,"mean_itl_ms":5.56}}`)
	assert.InDelta(t, (42-42)/(136.5/1000), u.Tokens.PromptPerSecond, 1e-9, "metrics prefill time")
	assert.InDelta(t, 1000/5.56, u.Tokens.TokensPerSecond, 1e-9, "metrics ITL")
}

func TestUsage_VLLM_IgnoresTimings(t *testing.T) {
	u := parseUsageFixture(t, hint.VLLM, "/v1/chat/completions", "llama-server/chat_completion.json")
	assert.Equal(t, 28, u.Tokens.InputTokens, "usage block is still read")
	assert.Equal(t, -1, u.Tokens.CachedTokens, "cache_n is llama-server's")
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
}
