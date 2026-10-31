package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

const tabbyAPIFinalChunk = `{"id":"chatcmpl-131b9995cd0c40c99e2ce958083ee406","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":83,"prompt_time":2.36,"prompt_tokens_per_sec":35.17,"completion_tokens":192,"completion_time":11.22,"completion_tokens_per_sec":17.11,"total_tokens":275,"total_time":13.63},"model":"SC_6.00bpw_H6_V6"}`

func TestServer_UsageTabbyAPI_Rates(t *testing.T) {
	u := parseUsageJSON(t, hint.TabbyAPI, "/v1/chat/completions", tabbyAPIFinalChunk)
	assert.Equal(t, 83, u.Tokens.InputTokens)
	assert.Equal(t, 192, u.Tokens.OutputTokens)
	assert.InDelta(t, 35.17, u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 17.11, u.Tokens.TokensPerSecond, 1e-9)
	assert.Equal(t, 13630, u.DurationMs, "total_time in seconds")
}

func TestServer_UsageTabbyAPI_RatesStream(t *testing.T) {
	u := parseUsageStream(t, hint.TabbyAPI, "/v1/chat/completions", contentChunk, tabbyAPIFinalChunk)
	assert.Equal(t, 83, u.Tokens.InputTokens)
	assert.InDelta(t, 35.17, u.Tokens.PromptPerSecond, 1e-9)
	assert.InDelta(t, 17.11, u.Tokens.TokensPerSecond, 1e-9)
}

func TestServer_UsageTabbyAPI_RateValues(t *testing.T) {
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

func TestServer_UsageTabbyAPI_IgnoresOtherServersFields(t *testing.T) {
	u := parseUsageJSON(t, hint.TabbyAPI, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":4,"completion_tokens":6,"prompt_tokens_per_second":12},"timings":{"prompt_n":40,"predicted_n":60,"predicted_per_second":5}}`)
	assert.Equal(t, 4, u.Tokens.InputTokens, "timings are llama-server's")
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond, "*_per_second is oMLX's")
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
}
