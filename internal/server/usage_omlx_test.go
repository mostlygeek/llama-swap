package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestServer_UsageOMLX_Rates(t *testing.T) {
	tests := []struct {
		name       string
		rates      string
		wantPrompt float64
		wantGen    float64
	}{
		{"reported", `,"prompt_tokens_per_second":63.74,"generation_tokens_per_second":66.38`, 63.74, 66.38},
		{"zero", `,"prompt_tokens_per_second":0,"generation_tokens_per_second":0`, 0, 0},
		{"null", `,"prompt_tokens_per_second":null,"generation_tokens_per_second":null`, -1, -1},
		{"missing", "", -1, -1},
		{"prompt only", `,"prompt_tokens_per_second":63.74`, 63.74, -1},
		{"generation only", `,"generation_tokens_per_second":66.38`, -1, 66.38},
		{"non-numeric", `,"prompt_tokens_per_second":"unknown","generation_tokens_per_second":false`, -1, -1},
	}
	for _, tt := range tests {
		body := `{"usage":{"prompt_tokens":56,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":16}` + tt.rates + `}}`
		t.Run(tt.name+"/JSON", func(t *testing.T) {
			u := parseUsageJSON(t, hint.OMLX, "/v1/chat/completions", body)
			assertOMLXUsage(t, u, tt.wantPrompt, tt.wantGen)
		})
		t.Run(tt.name+"/SSE", func(t *testing.T) {
			u := parseUsageStream(t, hint.OMLX, "/v1/chat/completions", contentChunk, body)
			assertOMLXUsage(t, u, tt.wantPrompt, tt.wantGen)
		})
	}
}

func assertOMLXUsage(t *testing.T, u Usage, wantPrompt, wantGen float64) {
	t.Helper()
	assert.Equal(t, 56, u.Tokens.InputTokens)
	assert.Equal(t, 8, u.Tokens.OutputTokens)
	assert.Equal(t, 16, u.Tokens.CachedTokens)
	assert.Equal(t, wantPrompt, u.Tokens.PromptPerSecond)
	assert.Equal(t, wantGen, u.Tokens.TokensPerSecond)
}

func TestServer_UsageOMLX_IgnoresTabbyAPIFields(t *testing.T) {
	u := parseUsageJSON(t, hint.OMLX, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":4,"completion_tokens":6,"prompt_tokens_per_sec":99,"completion_tokens_per_sec":99,"total_time":99}}`)
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond)
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
	assert.Less(t, u.DurationMs, 99000)
}
