package usage

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestUsage_TabbyAPI_ChatCompletion(t *testing.T) {
	u := parseUsageFixture(t, hint.TabbyAPI, "/v1/chat/completions", "tabbyapi/chat_completion.json")
	assert.Equal(t, 83, u.Tokens.InputTokens)
	assert.Equal(t, 192, u.Tokens.OutputTokens)
	assert.InDelta(t, 35.17, u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 17.11, u.Tokens.TokensPerSecond, 1e-9)
	assert.Equal(t, 13630, u.DurationMs, "total_time in seconds")
}

func TestUsage_TabbyAPI_ChatCompletionStream(t *testing.T) {
	u := parseUsageFixture(t, hint.TabbyAPI, "/v1/chat/completions", "tabbyapi/chat_completion_stream.sse")
	assert.Equal(t, 83, u.Tokens.InputTokens)
	assert.InDelta(t, 35.17, u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 17.11, u.Tokens.TokensPerSecond, 1e-9)
	assert.Equal(t, 13630, u.DurationMs)
}

func TestUsage_TabbyAPI_RateValues(t *testing.T) {
	tests := []struct {
		name       string
		rates      string
		wantPrompt float64
		wantGen    float64
	}{
		{"zero", `"prompt_tokens_per_sec":0,"completion_tokens_per_sec":0`, 0, 0},
		{"string", `"prompt_tokens_per_sec":"35.17","completion_tokens_per_sec":"17.11"`, 35.17, 17.11},
		{"null", `"prompt_tokens_per_sec":null,"completion_tokens_per_sec":null`, -1, -1},
		{"missing", `"x":1`, -1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := parseUsageJSON(t, hint.TabbyAPI, "/v1/chat/completions",
				`{"usage":{"prompt_tokens":1,"completion_tokens":1,`+tt.rates+`}}`)
			assert.InDelta(t, tt.wantPrompt, u.Tokens.PromptPerSecond, 1e-9)
			assert.InDelta(t, tt.wantGen, u.Tokens.TokensPerSecond, 1e-9)
		})
	}
}

func TestUsage_TabbyAPI_IgnoresOtherServersFields(t *testing.T) {
	llama := parseUsageFixture(t, hint.TabbyAPI, "/v1/chat/completions", "llama-server/chat_completion.json")
	assert.Equal(t, -1.0, llama.Tokens.TokensPerSecond, "timings are llama-server's")
	omlx := parseUsageFixture(t, hint.TabbyAPI, "/v1/chat/completions", "omlx/chat_completion.json")
	assert.Equal(t, -1.0, omlx.Tokens.PromptPerSecond, "*_per_second is oMLX's")
	assert.Equal(t, -1.0, omlx.Tokens.TokensPerSecond)
}
