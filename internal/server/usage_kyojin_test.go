package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestServer_UsageKyojin_ChatCompletionStream(t *testing.T) {
	u := parseUsageFixture(t, hint.Kyojin, "/v1/chat/completions", "kyojin/chat_completion_stream.sse")
	assert.True(t, u.Found)
	assert.Equal(t, 97, u.Tokens.InputTokens)
	assert.Equal(t, 25, u.Tokens.OutputTokens)
	assert.Equal(t, 0, u.Tokens.CachedTokens)
	assert.InDelta(t, 373.29396037196585, u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 49.61399936029313, u.Tokens.TokensPerSecond, 1e-9)
	assert.Equal(t, 763, u.DurationMs, "prompt_ms + predicted_ms")
	assert.Equal(t, 21, u.Tokens.DraftTokens, "accepted + rejected")
	assert.Equal(t, 15, u.Tokens.DraftAccTokens)
}

func TestServer_UsageKyojin_DraftsNeedBothCounters(t *testing.T) {
	u := parseUsageJSON(t, hint.Kyojin, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":1,"completion_tokens":1},"timings":{"prompt_n":1,"predicted_n":1,"accepted_draft_tokens":15}}`)
	assert.Equal(t, -1, u.Tokens.DraftTokens)
	assert.Equal(t, -1, u.Tokens.DraftAccTokens)
}

func TestServer_UsageKyojin_NotSpeculative(t *testing.T) {
	u := parseUsageJSON(t, hint.Kyojin, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":1,"completion_tokens":1},"timings":{"prompt_n":1,"predicted_n":1,"speculative":false}}`)
	assert.Equal(t, -1, u.Tokens.DraftTokens)
}
