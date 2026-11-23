package usage

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestUsage_FastFlowLM_JSON(t *testing.T) {
	u := parseUsageFixture(t, hint.OpenAICompatible, "/v1/chat/completions", "fastflowlm/chat_completion.json")
	assert.True(t, u.Found)
	assert.Equal(t, store.TokenMetrics{
		InputTokens: 14, CachedTokens: 0, OutputTokens: 128, ReasoningTokens: -1,
		PromptPerSecond: 18.850173051051524, TokensPerSecond: 16.14203782634345,
		DraftTokens: -1, DraftAccTokens: -1,
	}, u.Tokens)
}

func TestUsage_FastFlowLM_Stream(t *testing.T) {
	u := parseUsageStream(t, hint.OpenAICompatible, "/v1/chat/completions",
		`{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"}}],"usage":null}`,
		`{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`,
		`{"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":60,"prompt_tokens_details":{"cached_tokens":48},"completion_tokens":12,"total_tokens":72,"prefill_speed_tps":24.5,"decoding_speed_tps":8.5}}`)
	assert.True(t, u.Found)
	assert.Equal(t, store.TokenMetrics{
		InputTokens: 60, CachedTokens: 48, OutputTokens: 12, ReasoningTokens: -1,
		PromptPerSecond: 24.5, TokensPerSecond: 8.5,
		DraftTokens: -1, DraftAccTokens: -1,
	}, u.Tokens)
}
