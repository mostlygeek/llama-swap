package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestServer_UsageHalogen_ChatCompletionStream(t *testing.T) {
	u := parseUsageFixture(t, hint.Halogen, "/v1/chat/completions", "halogen/chat_completion_stream.sse")
	assert.True(t, u.Found)
	assert.Equal(t, 712, u.Tokens.InputTokens, "timings prompt_n")
	assert.Equal(t, 610, u.Tokens.OutputTokens)
	assert.Equal(t, 5188, u.Tokens.CachedTokens)
	assert.Equal(t, 93, u.Tokens.ReasoningTokens)
	assert.InDelta(t, 899.899, u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 50.627, u.Tokens.TokensPerSecond, 1e-9)
	assert.Equal(t, 12840, u.DurationMs, "prompt_ms + predicted_ms")
	assert.Equal(t, 484, u.Tokens.DraftTokens)
	assert.Equal(t, 332, u.Tokens.DraftAccTokens)
}
