package usage

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestUsage_OMLX_ChatCompletion(t *testing.T) {
	for _, file := range []string{"omlx/chat_completion.json", "omlx/chat_completion_stream.sse"} {
		t.Run(file, func(t *testing.T) {
			u := parseUsageFixture(t, hint.OMLX, "/v1/chat/completions", file)
			assert.Equal(t, 56, u.Tokens.InputTokens)
			assert.Equal(t, 8, u.Tokens.OutputTokens)
			assert.Equal(t, 16, u.Tokens.CachedTokens)
			assert.Equal(t, 63.74, u.Tokens.PromptPerSecond)
			assert.Equal(t, 66.38, u.Tokens.TokensPerSecond)
		})
	}
}

func TestUsage_OMLX_RateValues(t *testing.T) {
	tests := []struct {
		name       string
		rates      string
		wantPrompt float64
		wantGen    float64
	}{
		{"zero", `,"prompt_tokens_per_second":0,"generation_tokens_per_second":0`, 0, 0},
		{"null", `,"prompt_tokens_per_second":null,"generation_tokens_per_second":null`, -1, -1},
		{"missing", "", -1, -1},
		{"prompt only", `,"prompt_tokens_per_second":63.74`, 63.74, -1},
		{"generation only", `,"generation_tokens_per_second":66.38`, -1, 66.38},
		{"non-numeric", `,"prompt_tokens_per_second":"unknown","generation_tokens_per_second":false`, -1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := parseUsageJSON(t, hint.OMLX, "/v1/chat/completions",
				`{"usage":{"prompt_tokens":56,"completion_tokens":8`+tt.rates+`}}`)
			assert.Equal(t, tt.wantPrompt, u.Tokens.PromptPerSecond)
			assert.Equal(t, tt.wantGen, u.Tokens.TokensPerSecond)
		})
	}
}

func TestUsage_OMLX_IgnoresTabbyAPIFields(t *testing.T) {
	u := parseUsageFixture(t, hint.OMLX, "/v1/chat/completions", "tabbyapi/chat_completion.json")
	assert.Equal(t, 83, u.Tokens.InputTokens, "usage block is still read")
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond)
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
	assert.Less(t, u.DurationMs, 1000, "total_time is TabbyAPI's")
}
