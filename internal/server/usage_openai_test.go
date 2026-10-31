package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestServer_UsageOpenAI_ChatCompletion(t *testing.T) {
	u := parseUsageFixture(t, hint.OpenAI, "/v1/chat/completions", "openai/chat_completion.json")
	assert.True(t, u.Found)
	assert.Equal(t, 12, u.Tokens.InputTokens)
	assert.Equal(t, 7, u.Tokens.OutputTokens)
	assert.Equal(t, 4, u.Tokens.CachedTokens)
	assert.Equal(t, 0, u.Tokens.ReasoningTokens)
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond, "OpenAI reports no rates")
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
	assert.Equal(t, -1, u.Tokens.DraftTokens)
}

func TestServer_UsageOpenAI_ChatCompletionStream(t *testing.T) {
	// Chunks before the last carry "usage":null.
	u := parseUsageFixture(t, hint.OpenAI, "/v1/chat/completions", "openai/chat_completion_stream.sse")
	assert.Equal(t, 15, u.Tokens.InputTokens)
	assert.Equal(t, 33, u.Tokens.OutputTokens)
	assert.Equal(t, -1, u.Tokens.CachedTokens)
	assert.Equal(t, -1, u.Tokens.ReasoningTokens)
}

func TestServer_UsageOpenAI_ChatCompletionReasoning(t *testing.T) {
	u := parseUsageFixture(t, hint.OpenAI, "/v1/chat/completions", "openai/chat_completion_reasoning.json")
	assert.Equal(t, 9, u.Tokens.OutputTokens)
	assert.Equal(t, 7, u.Tokens.ReasoningTokens)
}

func TestServer_UsageOpenAI_Responses(t *testing.T) {
	u := parseUsageFixture(t, hint.OpenAI, "/v1/responses", "openai/responses.json")
	assert.Equal(t, 11, u.Tokens.InputTokens)
	assert.Equal(t, 5, u.Tokens.OutputTokens)
	assert.Equal(t, 3, u.Tokens.CachedTokens)
	assert.Equal(t, 2, u.Tokens.ReasoningTokens)
}

func TestServer_UsageOpenAI_ResponsesStream(t *testing.T) {
	// response.created has "usage":null; response.completed carries it.
	u := parseUsageFixture(t, hint.OpenAI, "/v1/responses", "openai/responses_stream.sse")
	assert.Equal(t, 11, u.Tokens.InputTokens)
	assert.Equal(t, 5, u.Tokens.OutputTokens)
	assert.Equal(t, 3, u.Tokens.CachedTokens)
	assert.Equal(t, 2, u.Tokens.ReasoningTokens)
}

func TestServer_UsageOpenAI_Embeddings(t *testing.T) {
	u := parseUsageFixture(t, hint.OpenAI, "/v1/embeddings", "openai/embeddings.json")
	assert.Equal(t, 8, u.Tokens.InputTokens)
	assert.Equal(t, 0, u.Tokens.OutputTokens)
}

func TestServer_UsageOpenAI_LaterZeroCountsKeepEarlierValues(t *testing.T) {
	u := parseUsageStream(t, hint.OpenAI, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":15,"completion_tokens":33}}`,
		`{"usage":{"prompt_tokens":0,"completion_tokens":0}}`)
	assert.Equal(t, 15, u.Tokens.InputTokens)
	assert.Equal(t, 33, u.Tokens.OutputTokens)
}

func TestServer_UsageOpenAI_ReasoningTokenValues(t *testing.T) {
	tests := []struct {
		name    string
		details string
		want    int
	}{
		{"zero is reported", `,"completion_tokens_details":{"reasoning_tokens":0}`, 0},
		{"missing", ``, -1},
		{"null", `,"completion_tokens_details":{"reasoning_tokens":null}`, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := parseUsageJSON(t, hint.OpenAI, "/v1/chat/completions",
				`{"usage":{"prompt_tokens":5,"completion_tokens":9`+tt.details+`}}`)
			assert.Equal(t, tt.want, u.Tokens.ReasoningTokens)
		})
	}
}

func TestServer_UsageOpenAI_IgnoresOtherServersFields(t *testing.T) {
	// TabbyAPI rates, llama-server timings and vLLM metrics are not part of
	// the OpenAI format, so the API extractor leaves them alone.
	for _, file := range []string{
		"tabbyapi/chat_completion.json",
		"llama-server/chat_completion.json",
		"vllm/chat_completion.json",
		"omlx/chat_completion.json",
	} {
		t.Run(file, func(t *testing.T) {
			u := parseUsageFixture(t, hint.OpenAI, "/v1/chat/completions", file)
			assert.Equal(t, -1.0, u.Tokens.PromptPerSecond)
			assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
			assert.Less(t, u.DurationMs, 1000, "duration is wall time only")
		})
	}
}
