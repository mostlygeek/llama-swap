package usage

import (
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Source-derived examples, not recorded GPU responses. Shapes verified against
// hipfire-cli/src/serve/complete.rs (completion_usage/completion_timings) and
// serve/anthropic.rs at Hipfire efa8fbb1d.
const hipfireChat = `{"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19,"prompt_tokens_details":{"cached_tokens":4}},"timings":{"ttft_ms":50,"prefill_ms":40,"prefill_tok_s":200,"decode_tok_s":40,"latency_ms":60000,"tau":2.5,"cycles":3},"hipfire":{"tok_s":20}}`

func TestUsage_Hipfire_Chat(t *testing.T) {
	for _, upstream := range []hint.Upstream{hint.Hipfire, hint.Unknown, hint.OpenAICompatible, hint.LlamaSwap} {
		t.Run(string(upstream), func(t *testing.T) {
			for _, streaming := range []bool{false, true} {
				var u Usage
				if streaming {
					u = parseUsageStream(t, upstream, "/v1/chat/completions", contentChunk, hipfireChat)
				} else {
					u = parseUsageJSON(t, upstream, "/v1/chat/completions", hipfireChat)
				}
				assert.True(t, u.Found)
				assert.Equal(t, 12, u.Tokens.InputTokens, "prompt already includes cached tokens")
				assert.Equal(t, 7, u.Tokens.OutputTokens)
				assert.Equal(t, 4, u.Tokens.CachedTokens)
				assert.Equal(t, 200.0, u.Tokens.PromptPerSecond)
				assert.Equal(t, 40.0, u.Tokens.TokensPerSecond)
				assert.Equal(t, -1, u.Tokens.ReasoningTokens)
				assert.Equal(t, -1, u.Tokens.DraftTokens, "tau/cycles are not draft counters")
				assert.Equal(t, -1, u.Tokens.DraftAccTokens)
				assert.Equal(t, 60000, u.DurationMs)
			}
		})
	}
}

func TestUsage_Hipfire_Rates(t *testing.T) {
	for _, tc := range []struct {
		name, timings  string
		prompt, decode float64
	}{
		{"missing", `{}`, -1, -1},
		{"null", `{"prefill_tok_s":null,"decode_tok_s":null}`, -1, -1},
		{"string", `{"prefill_tok_s":"200","decode_tok_s":"40"}`, -1, -1},
		{"negative", `{"prefill_tok_s":-2,"decode_tok_s":-1}`, -1, -1},
		{"zero", `{"prefill_tok_s":0,"decode_tok_s":0}`, 0, 0},
		{"prefill only", `{"prefill_tok_s":200}`, 200, -1},
		{"decode only", `{"decode_tok_s":40}`, -1, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"usage":{"prompt_tokens":12,"completion_tokens":7},"timings":` + tc.timings + `,"hipfire":{"tok_s":99}}`
			for _, upstream := range []hint.Upstream{hint.Hipfire, hint.Unknown} {
				for _, u := range []Usage{
					parseUsageJSON(t, upstream, "/v1/chat/completions", body),
					parseUsageStream(t, upstream, "/v1/chat/completions", contentChunk, body),
				} {
					assert.Equal(t, 12, u.Tokens.InputTokens)
					assert.Equal(t, 7, u.Tokens.OutputTokens)
					assert.Equal(t, tc.prompt, u.Tokens.PromptPerSecond)
					assert.Equal(t, tc.decode, u.Tokens.TokensPerSecond, "never fall back to wall-inclusive hipfire.tok_s")
				}
			}
		})
	}
}

func TestUsage_Hipfire_Anthropic(t *testing.T) {
	// Hipfire uses the same timings projection in Messages responses and
	// message_delta events. Anthropic input_tokens excludes cache reads.
	for _, upstream := range []hint.Upstream{hint.Hipfire, hint.Unknown} {
		body := `{"usage":{"input_tokens":8,"output_tokens":7,"cache_read_input_tokens":4},"timings":{"prefill_tok_s":200,"decode_tok_s":40}}`
		json := parseUsageJSON(t, upstream, "/v1/messages", body)
		stream := parseUsageStream(t, upstream, "/v1/messages",
			`{"type":"message_start","message":{"usage":{"input_tokens":8,"output_tokens":0,"cache_read_input_tokens":4}}}`,
			`{"type":"message_delta","usage":{"output_tokens":7},"timings":{"prefill_tok_s":200,"decode_tok_s":40}}`,
			`{"type":"message_stop"}`)
		assert.Equal(t, json.Tokens, stream.Tokens)
		assert.Equal(t, 12, stream.Tokens.InputTokens)
		assert.Equal(t, 4, stream.Tokens.CachedTokens)
		assert.Equal(t, 7, stream.Tokens.OutputTokens)
		assert.Equal(t, 200.0, stream.Tokens.PromptPerSecond)
		assert.Equal(t, 40.0, stream.Tokens.TokensPerSecond)
	}
}

func TestUsage_Hipfire_DurationKeepsWallTime(t *testing.T) {
	u, err := Extract(hint.Hipfire, "/v1/chat/completions", "application/json",
		[]byte(`{"usage":{"prompt_tokens":1},"timings":{"latency_ms":1}}`), time.Now().Add(-time.Second))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, u.DurationMs, 1000)
}
