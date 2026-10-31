package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestServer_UsageRadiance_ChatCompletionStream(t *testing.T) {
	u := parseUsageFixture(t, hint.Radiance, "/v1/chat/completions", "radiance/chat_completion_stream.sse")
	assert.True(t, u.Found)
	assert.Equal(t, 56, u.Tokens.InputTokens)
	assert.Equal(t, 25, u.Tokens.OutputTokens)
	assert.Equal(t, 0, u.Tokens.CachedTokens)
	assert.Equal(t, 25, u.Tokens.ReasoningTokens)
	assert.InDelta(t, 1396.9499692845627, u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 122.55872718022886, u.Tokens.TokensPerSecond, 1e-9)
	assert.Equal(t, 42, u.Tokens.DraftTokens)
	assert.Equal(t, 18, u.Tokens.DraftAccTokens)
	assert.Equal(t, 237, u.DurationMs, "total_ms, longer than prompt_ms + predicted_ms")
}

func TestServer_UsageRadiance_WithoutTotal(t *testing.T) {
	u := parseUsageJSON(t, hint.Radiance, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":1,"completion_tokens":1},"timings":{"prompt_n":1,"predicted_n":1,"prompt_ms":100,"predicted_ms":200}}`)
	assert.Equal(t, 300, u.DurationMs, "falls back to prompt_ms + predicted_ms")
}

func TestServer_UsageRadiance_MessagesStream(t *testing.T) {
	// Radiance reports input_tokens in message_delta, where Anthropic puts
	// it in message_start.
	u := parseUsageFixture(t, hint.Radiance, "/v1/messages", "radiance/messages_stream.sse")
	assert.True(t, u.Found)
	assert.Equal(t, 85, u.Tokens.InputTokens)
	assert.Equal(t, 22, u.Tokens.OutputTokens)
	assert.Equal(t, 0, u.Tokens.CachedTokens)
	assert.Equal(t, -1, u.Tokens.ReasoningTokens)
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond, "no timings on /v1/messages")
}

func TestServer_UsageRadiance_ResponsesStream(t *testing.T) {
	u := parseUsageFixture(t, hint.Radiance, "/v1/responses", "radiance/responses_stream.sse")
	assert.True(t, u.Found)
	assert.Equal(t, 85, u.Tokens.InputTokens)
	assert.Equal(t, 35, u.Tokens.OutputTokens)
	assert.Equal(t, 0, u.Tokens.CachedTokens)
	assert.Equal(t, 32, u.Tokens.ReasoningTokens)
}
