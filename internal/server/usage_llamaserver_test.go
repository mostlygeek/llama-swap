package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

const llamaServerTimings = `"timings":{"prompt_n":20,"predicted_n":50,"prompt_per_second":100.0,"predicted_per_second":40.0,"prompt_ms":200,"predicted_ms":1250,"cache_n":8}`

func TestServer_UsageLlamaServer_Timings(t *testing.T) {
	u := parseUsageJSON(t, hint.LlamaServer, "/v1/chat/completions", `{`+llamaServerTimings+`}`)
	assert.True(t, u.Found, "timings alone count as usage")
	assert.Equal(t, 20, u.Tokens.InputTokens)
	assert.Equal(t, 50, u.Tokens.OutputTokens)
	assert.Equal(t, 8, u.Tokens.CachedTokens)
	assert.Equal(t, 100.0, u.Tokens.PromptPerSecond)
	assert.Equal(t, 40.0, u.Tokens.TokensPerSecond)
	assert.Equal(t, 1450, u.DurationMs, "prompt_ms + predicted_ms")
}

func TestServer_UsageLlamaServer_TimingsOverrideUsage(t *testing.T) {
	u := parseUsageJSON(t, hint.LlamaServer, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":1,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":0}},`+llamaServerTimings+`}`)
	assert.Equal(t, 20, u.Tokens.InputTokens)
	assert.Equal(t, 50, u.Tokens.OutputTokens)
	assert.Equal(t, 8, u.Tokens.CachedTokens)
}

func TestServer_UsageLlamaServer_TimingsStream(t *testing.T) {
	u := parseUsageStream(t, hint.LlamaServer, "/v1/chat/completions",
		contentChunk,
		`{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":50},`+llamaServerTimings+`}`)
	assert.Equal(t, 20, u.Tokens.InputTokens)
	assert.Equal(t, 40.0, u.Tokens.TokensPerSecond)
}

func TestServer_UsageLlamaServer_DraftTokens(t *testing.T) {
	u := parseUsageJSON(t, hint.LlamaServer, "/v1/chat/completions",
		`{"timings":{"prompt_n":20,"predicted_n":50,"draft_n":30,"draft_n_accepted":12}}`)
	assert.Equal(t, 30, u.Tokens.DraftTokens)
	assert.Equal(t, 12, u.Tokens.DraftAccTokens)
}

func TestServer_UsageLlamaServer_DraftTokensNeedBothCounters(t *testing.T) {
	u := parseUsageJSON(t, hint.LlamaServer, "/v1/chat/completions",
		`{"timings":{"prompt_n":20,"predicted_n":50,"draft_n":30}}`)
	assert.Equal(t, -1, u.Tokens.DraftTokens)
	assert.Equal(t, -1, u.Tokens.DraftAccTokens)
}

func TestServer_UsageLlamaServer_Infill(t *testing.T) {
	// /infill responses are arrays; timings live in the last element (#463).
	u := parseUsageJSON(t, hint.LlamaServer, "/infill",
		`[{"content":"a"},{"content":"b","timings":{"prompt_n":5,"predicted_n":9,"prompt_ms":10,"predicted_ms":20}}]`)
	assert.True(t, u.Found)
	assert.Equal(t, 5, u.Tokens.InputTokens)
	assert.Equal(t, 9, u.Tokens.OutputTokens)
}

func TestServer_UsageLlamaServer_EmptyInfillArray(t *testing.T) {
	u := parseUsageJSON(t, hint.LlamaServer, "/infill", `[]`)
	assert.False(t, u.Found)
}

func TestServer_UsageLlamaServer_IgnoresVLLMMetrics(t *testing.T) {
	u := parseUsageJSON(t, hint.LlamaServer, "/v1/chat/completions",
		`{`+llamaServerTimings+`,"metrics":{"time_to_first_token_ms":1000,"mean_itl_ms":500}}`)
	assert.Equal(t, 100.0, u.Tokens.PromptPerSecond)
	assert.Equal(t, 40.0, u.Tokens.TokensPerSecond)
}
