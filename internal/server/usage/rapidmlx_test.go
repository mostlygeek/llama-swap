package usage

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestUsage_RapidMLX_ChatCompletion(t *testing.T) {
	u := parseUsageFixture(t, hint.RapidMLX, "/v1/chat/completions", "rapid-mlx/chat_completion.json")
	assert.True(t, u.Found)
	assert.Equal(t, 16, u.Tokens.InputTokens)
	assert.Equal(t, 1, u.Tokens.OutputTokens)
	assert.Equal(t, -1, u.Tokens.CachedTokens)
	assert.Equal(t, -1, u.Tokens.ReasoningTokens)
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond)
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
	assert.Equal(t, -1, u.Tokens.DraftTokens)
	assert.Equal(t, -1, u.Tokens.DraftAccTokens)

	generic := parseUsageFixture(t, hint.OpenAICompatible, "/v1/chat/completions", "rapid-mlx/chat_completion.json")
	assert.Equal(t, generic.Found, u.Found)
	assert.Equal(t, generic.Tokens, u.Tokens, "identification preserves generic usage extraction")
}
