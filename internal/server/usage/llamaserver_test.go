package usage

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestUsage_LlamaServer_ChatCompletion(t *testing.T) {
	u := parseUsageFixture(t, hint.LlamaServer, "/v1/chat/completions", "llama-server/chat_completion.json")
	assert.True(t, u.Found)
	assert.Equal(t, 28, u.Tokens.InputTokens, "prompt_n + cache_n")
	assert.Equal(t, 50, u.Tokens.OutputTokens)
	assert.Equal(t, 8, u.Tokens.CachedTokens, "cache_n")
	assert.Equal(t, 100.0, u.Tokens.PromptPerSecond)
	assert.Equal(t, 40.0, u.Tokens.TokensPerSecond)
	assert.Equal(t, 1450, u.DurationMs, "prompt_ms + predicted_ms")
}

func TestUsage_LlamaServer_ChatCompletionStream(t *testing.T) {
	u := parseUsageFixture(t, hint.LlamaServer, "/v1/chat/completions", "llama-server/chat_completion_stream.sse")
	assert.Equal(t, 28, u.Tokens.InputTokens)
	assert.Equal(t, 50, u.Tokens.OutputTokens)
	assert.Equal(t, 40.0, u.Tokens.TokensPerSecond)
}

func TestUsage_LlamaServer_DraftTokens(t *testing.T) {
	u := parseUsageFixture(t, hint.LlamaServer, "/v1/chat/completions", "llama-server/chat_completion_draft.json")
	assert.Equal(t, 30, u.Tokens.DraftTokens)
	assert.Equal(t, 12, u.Tokens.DraftAccTokens)
}

func TestUsage_LlamaServer_Infill(t *testing.T) {
	// /infill responses are arrays; timings live in the last element (#463).
	u := parseUsageFixture(t, hint.LlamaServer, "/infill", "llama-server/infill.json")
	assert.True(t, u.Found)
	assert.Equal(t, 5, u.Tokens.InputTokens)
	assert.Equal(t, 9, u.Tokens.OutputTokens)
	assert.Equal(t, 450.0, u.Tokens.TokensPerSecond)
}

func TestUsage_LlamaServer_TimingsOverrideUsage(t *testing.T) {
	u := parseUsageJSON(t, hint.LlamaServer, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":1,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":0}},`+
			`"timings":{"prompt_n":20,"predicted_n":50,"cache_n":8}}`)
	assert.Equal(t, 28, u.Tokens.InputTokens, "prompt_n counts only processed tokens")
	assert.Equal(t, 50, u.Tokens.OutputTokens)
	assert.Equal(t, 8, u.Tokens.CachedTokens)
}

func TestUsage_LlamaServer_DraftTokensNeedBothCounters(t *testing.T) {
	u := parseUsageJSON(t, hint.LlamaServer, "/v1/chat/completions",
		`{"timings":{"prompt_n":20,"predicted_n":50,"draft_n":30}}`)
	assert.Equal(t, -1, u.Tokens.DraftTokens)
	assert.Equal(t, -1, u.Tokens.DraftAccTokens)
}

func TestUsage_LlamaServer_EmptyInfillArray(t *testing.T) {
	u := parseUsageJSON(t, hint.LlamaServer, "/infill", `[]`)
	assert.False(t, u.Found)
}

func TestUsage_LlamaServer_IgnoresVLLMMetrics(t *testing.T) {
	u := parseUsageFixture(t, hint.LlamaServer, "/v1/chat/completions", "vllm/chat_completion.json")
	assert.Equal(t, 42, u.Tokens.InputTokens, "usage block is still read")
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond, "metrics are vLLM's")
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
}
