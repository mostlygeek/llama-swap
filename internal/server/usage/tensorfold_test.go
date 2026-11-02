package usage

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestUsage_TensorFold_ChatCompletion(t *testing.T) {
	tests := []struct {
		file                   string
		input, output          int
		prefillSecs, decodeSec float64
	}{
		{"tensorfold/chat_completion.json", 63, 64, 0.1146, 2.0696},
		{"tensorfold/chat_completion_stream.sse", 59, 32, 0.2393, 0.7758},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			u := parseUsageFixture(t, hint.TensorFold, "/v1/chat/completions", tt.file)
			assert.True(t, u.Found)
			assert.Equal(t, tt.input, u.Tokens.InputTokens)
			assert.Equal(t, tt.output, u.Tokens.OutputTokens)
			assert.Equal(t, 0, u.Tokens.CachedTokens)
			assert.InDelta(t, float64(tt.input)/tt.prefillSecs, u.Tokens.PromptPerSecond, 1e-9, "uncached prompt / prefill_s")
			assert.InDelta(t, float64(tt.output)/tt.decodeSec, u.Tokens.TokensPerSecond, 1e-9, "output / decode_s")
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
