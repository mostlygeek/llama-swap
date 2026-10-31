package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

// The auto extractor reads every server's fields. These tests cover the
// order it applies them in when more than one appears: oMLX rates, then
// TabbyAPI rates, then llama-server timings, then vLLM metrics.

func TestServer_UsageAuto_ReadsEachServer(t *testing.T) {
	// Every server's fixture gives the same result through auto as through
	// its own extractor.
	tests := []struct {
		upstream hint.Upstream
		path     string
		file     string
	}{
		{hint.OpenAI, "/v1/chat/completions", "openai/chat_completion.json"},
		{hint.OpenAI, "/v1/responses", "openai/responses_stream.sse"},
		{hint.Anthropic, "/v1/messages", "anthropic/message_stream.sse"},
		{hint.LlamaServer, "/v1/chat/completions", "llama-server/chat_completion.json"},
		{hint.LlamaServer, "/infill", "llama-server/infill.json"},
		{hint.VLLM, "/v1/chat/completions", "vllm/chat_completion.json"},
		{hint.VLLM, "/v1/chat/completions", "vllm/chat_completion_spec_decode_stream.sse"},
		{hint.TabbyAPI, "/v1/chat/completions", "tabbyapi/chat_completion_stream.sse"},
		{hint.OMLX, "/v1/chat/completions", "omlx/chat_completion.json"},
		{hint.Kyojin, "/v1/chat/completions", "kyojin/chat_completion_stream.sse"},
		{hint.Radiance, "/v1/chat/completions", "radiance/chat_completion_stream.sse"},
		{hint.Radiance, "/v1/messages", "radiance/messages_stream.sse"},
		{hint.Radiance, "/v1/responses", "radiance/responses_stream.sse"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			want := parseUsageFixture(t, tt.upstream, tt.path, tt.file)
			got := parseUsageFixture(t, hint.Unknown, tt.path, tt.file)
			assert.Equal(t, want.Found, got.Found)
			assert.Equal(t, want.Tokens, got.Tokens)
		})
	}
}

func TestServer_UsageAuto_Precedence(t *testing.T) {
	const omlx = `,"prompt_tokens_per_second":63.74,"generation_tokens_per_second":66.38`
	const timings = `,"timings":{"prompt_n":56,"predicted_n":8,"cache_n":16,"prompt_per_second":100,"predicted_per_second":40}`
	const metrics = `,"metrics":{"time_to_first_token_ms":200,"mean_itl_ms":10}`
	tests := []struct {
		name       string
		rates      string
		extra      string
		wantPrompt float64
		wantGen    float64
	}{
		{"TabbyAPI over oMLX", `,"prompt_tokens_per_sec":35.17,"completion_tokens_per_sec":17.11` + omlx, "", 35.17, 17.11},
		{"TabbyAPI zero over oMLX", `,"prompt_tokens_per_sec":0,"completion_tokens_per_sec":0` + omlx, "", 0, 0},
		{"TabbyAPI null falls back to oMLX", `,"prompt_tokens_per_sec":null,"completion_tokens_per_sec":null` + omlx, "", 63.74, 66.38},
		{"per field: TabbyAPI prompt, oMLX generation", `,"prompt_tokens_per_sec":35.17` + omlx, "", 35.17, 66.38},
		{"per field: oMLX prompt, TabbyAPI generation", `,"completion_tokens_per_sec":17.11` + omlx, "", 63.74, 17.11},
		{"timings over usage rates", omlx, timings, 100, 40},
		{"vLLM metrics over usage rates", omlx, metrics, 200, 100},
		{"vLLM metrics over timings", omlx, timings + metrics, 200, 100},
	}
	for _, tt := range tests {
		body := `{"usage":{"prompt_tokens":56,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":16}` + tt.rates + `}` + tt.extra + `}`
		t.Run(tt.name+"/JSON", func(t *testing.T) {
			u := parseUsageJSON(t, hint.Unknown, "/v1/chat/completions", body)
			assert.Equal(t, tt.wantPrompt, u.Tokens.PromptPerSecond)
			assert.Equal(t, tt.wantGen, u.Tokens.TokensPerSecond)
		})
		t.Run(tt.name+"/SSE", func(t *testing.T) {
			u := parseUsageStream(t, hint.Unknown, "/v1/chat/completions", contentChunk, body)
			assert.Equal(t, tt.wantPrompt, u.Tokens.PromptPerSecond)
			assert.Equal(t, tt.wantGen, u.Tokens.TokensPerSecond)
		})
	}
}

func TestServer_UsageAuto_TimingsDraftTokensSurviveEmptyMetrics(t *testing.T) {
	// An empty speculative_decoding object must not clobber timings' counts.
	u := parseUsageJSON(t, hint.Unknown, "/v1/chat/completions",
		`{"timings":{"prompt_n":20,"predicted_n":50,"draft_n":30,"draft_n_accepted":12},"metrics":{"speculative_decoding":{}}}`)
	assert.Equal(t, 30, u.Tokens.DraftTokens)
	assert.Equal(t, 12, u.Tokens.DraftAccTokens)
}
