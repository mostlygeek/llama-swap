package usage

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestUsage_TensorFold_ChatCompletion(t *testing.T) {
	tests := []struct {
		file          string
		input, output int
		wantPrompt    float64
		wantGen       float64
	}{
		{"tensorfold/chat_completion.json", 63, 64, 63 / 0.1146, 64 / 2.0696},
		{"tensorfold/chat_completion_stream.sse", 59, 32, 59 / 0.2393, 32 / 0.7758},
		{"tensorfold/chat_completion_stream_v2.sse", 160029, 11811, 160029 / 57.476540016, 46.31119504068745},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			u := parseUsageFixture(t, hint.TensorFold, "/v1/chat/completions", tt.file)
			assert.True(t, u.Found)
			assert.Equal(t, tt.input, u.Tokens.InputTokens)
			assert.Equal(t, tt.output, u.Tokens.OutputTokens)
			assert.Equal(t, 0, u.Tokens.CachedTokens)
			assert.InDelta(t, tt.wantPrompt, u.Tokens.PromptPerSecond, 1e-9, "uncached prompt / prefill duration")
			assert.InDelta(t, tt.wantGen, u.Tokens.TokensPerSecond, 1e-9, "server rate or output / decode_s")
			assert.Equal(t, -1, u.Tokens.DraftTokens, "drafted/accepted are not mapped")
			assert.Equal(t, -1, u.Tokens.DraftAccTokens)
		})
	}
}

func TestUsage_TensorFold_Values(t *testing.T) {
	const usageBlock = `"prompt_tokens":56,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":16}`
	tests := []struct {
		name       string
		tensorfold string
		wantCached int
		wantPrompt float64
		wantGen    float64
	}{
		{"cached overrides usage", `{"prefill_s":0.5,"decode_s":2.0,"cached":0}`, 0, 112, 4},
		{"uncached subtracted", `{"prefill_s":0.4,"decode_s":2.0,"cached":16}`, 16, 100, 4},
		{"zero durations", `{"prefill_s":0,"decode_s":0,"cached":0}`, 0, -1, -1},
		{"negative durations", `{"prefill_s":-1,"decode_s":-1,"cached":0}`, 0, -1, -1},
		{"null durations", `{"prefill_s":null,"decode_s":null,"cached":0}`, 0, -1, -1},
		{"string durations", `{"prefill_s":"0.5","decode_s":"2.0","cached":0}`, 0, -1, -1},
		{"overflowing durations", `{"prefill_s":1e-320,"decode_s":1e-320,"cached":0}`, 0, -1, -1},
		{"empty object", `{}`, 16, -1, -1},
		{"prefill only", `{"prefill_s":0.5}`, 16, 80, -1},
		{"decode only", `{"decode_s":2.0}`, 16, -1, 4},
		{"negative cached ignored", `{"prefill_s":0.5,"decode_s":2.0,"cached":-1}`, 16, 80, 4},
		{"null cached ignored", `{"prefill_s":0.5,"decode_s":2.0,"cached":null}`, 16, 80, 4},
		{"v2 names", `{"prefill_seconds":0.5,"seconds":30,"tokens_per_second":46.31}`, 16, 80, 46.31},
		{"v2 seconds alone ignored", `{"seconds":30,"time_to_first_token":1.0}`, 16, -1, -1},
		{"v2 rate wins over decode_s", `{"tokens_per_second":46.31,"decode_s":2.0}`, 16, -1, 46.31},
		{"v2 invalid rate falls back to decode_s", `{"tokens_per_second":0,"decode_s":2.0}`, 16, -1, 4},
		{"v2 negative rate falls back to decode_s", `{"tokens_per_second":-1,"decode_s":2.0}`, 16, -1, 4},
		{"v2 string rate ignored", `{"tokens_per_second":"46.3"}`, 16, -1, -1},
		{"prefill_s wins over prefill_seconds", `{"prefill_s":0.5,"prefill_seconds":2.0}`, 16, 80, -1},
	}
	for _, tt := range tests {
		body := `{"usage":{` + usageBlock + `},"tensorfold":` + tt.tensorfold + `}`
		check := func(t *testing.T, u Usage) {
			assert.True(t, u.Found)
			assert.Equal(t, 56, u.Tokens.InputTokens)
			assert.Equal(t, 8, u.Tokens.OutputTokens)
			assert.Equal(t, tt.wantCached, u.Tokens.CachedTokens)
			assert.Equal(t, tt.wantPrompt, u.Tokens.PromptPerSecond)
			assert.Equal(t, tt.wantGen, u.Tokens.TokensPerSecond)
		}
		t.Run(tt.name+"/JSON", func(t *testing.T) {
			check(t, parseUsageJSON(t, hint.TensorFold, "/v1/chat/completions", body))
		})
		t.Run(tt.name+"/SSE", func(t *testing.T) {
			check(t, parseUsageStream(t, hint.TensorFold, "/v1/chat/completions", contentChunk, body))
		})
	}
}

func TestUsage_TensorFold_MissingObject(t *testing.T) {
	u := parseUsageJSON(t, hint.TensorFold, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":56,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":16}}}`)
	assert.Equal(t, 16, u.Tokens.CachedTokens)
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond)
	assert.Equal(t, -1.0, u.Tokens.TokensPerSecond)
}

func TestUsage_TensorFold_CachedWithoutUsage(t *testing.T) {
	u := parseUsageJSON(t, hint.TensorFold, "/v1/chat/completions", `{"tensorfold":{"cached":9}}`)
	assert.True(t, u.Found)
	assert.Equal(t, 9, u.Tokens.CachedTokens)
	assert.Equal(t, -1.0, u.Tokens.PromptPerSecond)
}

func TestUsage_TensorFold_UsageArrivesAfterObject(t *testing.T) {
	// The object is on the finish_reason chunk; usage comes in the next one.
	u := parseUsageStream(t, hint.TensorFold, "/v1/chat/completions",
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"tensorfold":{"prefill_s":0.5,"decode_s":2,"cached":0}}`,
		`{"choices":[],"usage":{"prompt_tokens":56,"completion_tokens":8}}`)
	assert.Equal(t, 112.0, u.Tokens.PromptPerSecond)
	assert.Equal(t, 4.0, u.Tokens.TokensPerSecond)
}
