package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestServer_UsageOpenAI_ChatCompletions(t *testing.T) {
	u := parseUsageJSON(t, hint.OpenAI, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":12,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}}`)
	assert.True(t, u.Found)
	assert.Equal(t, 12, u.Tokens.InputTokens)
	assert.Equal(t, 7, u.Tokens.OutputTokens)
	assert.Equal(t, 4, u.Tokens.CachedTokens)
	assert.Equal(t, -1, u.Tokens.ReasoningTokens)
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond, "OpenAI reports no rates")
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
	assert.Equal(t, -1, u.Tokens.DraftTokens)
}

func TestServer_UsageOpenAI_ChatCompletionsStream(t *testing.T) {
	u := parseUsageStream(t, hint.OpenAI, "/v1/chat/completions",
		contentChunk,
		`{"choices":[],"usage":{"prompt_tokens":15,"completion_tokens":33}}`)
	assert.Equal(t, 15, u.Tokens.InputTokens)
	assert.Equal(t, 33, u.Tokens.OutputTokens)
	assert.Equal(t, -1, u.Tokens.CachedTokens)
}

func TestServer_UsageOpenAI_LaterZeroCountsKeepEarlierValues(t *testing.T) {
	u := parseUsageStream(t, hint.OpenAI, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":15,"completion_tokens":33}}`,
		`{"usage":{"prompt_tokens":0,"completion_tokens":0}}`)
	assert.Equal(t, 15, u.Tokens.InputTokens)
	assert.Equal(t, 33, u.Tokens.OutputTokens)
}

func TestServer_UsageOpenAI_ReasoningTokens(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"reported", `{"usage":{"prompt_tokens":5,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":7}}}`, 7},
		{"zero is reported", `{"usage":{"prompt_tokens":5,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":0}}}`, 0},
		{"missing", `{"usage":{"prompt_tokens":5,"completion_tokens":9}}`, -1},
		{"not a number", `{"usage":{"prompt_tokens":5,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":null}}}`, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := parseUsageJSON(t, hint.OpenAI, "/v1/chat/completions", tt.body)
			assert.Equal(t, tt.want, u.Tokens.ReasoningTokens)
		})
	}
}

func TestServer_UsageOpenAI_Responses(t *testing.T) {
	u := parseUsageJSON(t, hint.OpenAI, "/v1/responses",
		`{"object":"response","usage":{"input_tokens":11,"output_tokens":5,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}`)
	assert.Equal(t, 11, u.Tokens.InputTokens)
	assert.Equal(t, 5, u.Tokens.OutputTokens)
	assert.Equal(t, 3, u.Tokens.CachedTokens)
	assert.Equal(t, 2, u.Tokens.ReasoningTokens)
}

func TestServer_UsageOpenAI_ResponsesStream(t *testing.T) {
	u := parseUsageStream(t, hint.OpenAI, "/v1/responses",
		`{"type":"response.output_text.delta","delta":"hi"}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":5,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}}`)
	assert.Equal(t, 11, u.Tokens.InputTokens)
	assert.Equal(t, 5, u.Tokens.OutputTokens)
	assert.Equal(t, 3, u.Tokens.CachedTokens)
	assert.Equal(t, 2, u.Tokens.ReasoningTokens)
}

func TestServer_UsageOpenAI_Embeddings(t *testing.T) {
	u := parseUsageJSON(t, hint.OpenAI, "/v1/embeddings",
		`{"object":"list","data":[],"usage":{"prompt_tokens":8,"total_tokens":8}}`)
	assert.Equal(t, 8, u.Tokens.InputTokens)
	assert.Equal(t, 0, u.Tokens.OutputTokens)
}

func TestServer_UsageOpenAI_IgnoresOtherServersFields(t *testing.T) {
	// TabbyAPI rates, llama-server timings and vLLM metrics are not part of
	// the OpenAI format, so the API extractor leaves them alone.
	start := `{"usage":{"prompt_tokens":4,"completion_tokens":6,"prompt_tokens_per_sec":9,"prompt_tokens_per_second":9,"total_time":99},` +
		`"timings":{"prompt_n":40,"predicted_n":60,"predicted_per_second":5},` +
		`"metrics":{"time_to_first_token_ms":100,"mean_itl_ms":20}}`
	u := parseUsageJSON(t, hint.OpenAI, "/v1/chat/completions", start)
	assert.Equal(t, 4, u.Tokens.InputTokens)
	assert.Equal(t, 6, u.Tokens.OutputTokens)
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond)
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
	assert.Less(t, u.DurationMs, 99000)
}
