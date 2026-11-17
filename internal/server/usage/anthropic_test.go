package usage

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestUsage_Anthropic_Message(t *testing.T) {
	u := parseUsageFixture(t, hint.Anthropic, "/v1/messages", "anthropic/message.json")
	assert.True(t, u.Found)
	assert.Equal(t, 9, u.Tokens.InputTokens, "input_tokens + cache_read_input_tokens")
	assert.Equal(t, 3, u.Tokens.OutputTokens)
	assert.Equal(t, 2, u.Tokens.CachedTokens)
	// Thinking tokens are part of output_tokens and not broken out.
	assert.Equal(t, -1, u.Tokens.ReasoningTokens)
}

func TestUsage_Anthropic_MessageStream(t *testing.T) {
	// message_start carries input and cache counts; message_delta the final
	// output count.
	u := parseUsageFixture(t, hint.Anthropic, "/v1/messages", "anthropic/message_stream.sse")
	assert.Equal(t, 35, u.Tokens.InputTokens, "input_tokens + cache_read_input_tokens")
	assert.Equal(t, 42, u.Tokens.OutputTokens)
	assert.Equal(t, 10, u.Tokens.CachedTokens)
}

func TestUsage_Anthropic_ZeroCacheIsReported(t *testing.T) {
	u := parseUsageJSON(t, hint.Anthropic, "/v1/messages",
		`{"usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":0}}`)
	assert.Equal(t, 0, u.Tokens.CachedTokens)
}

func TestUsage_Anthropic_InputIncludesCacheWrites(t *testing.T) {
	// Anthropic's input_tokens leaves out tokens read from and written to
	// the cache; the normalized input is the whole prompt.
	u := parseUsageJSON(t, hint.Anthropic, "/v1/messages",
		`{"usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":2,"cache_creation_input_tokens":5}}`)
	assert.Equal(t, 14, u.Tokens.InputTokens)
	assert.Equal(t, 2, u.Tokens.CachedTokens, "cache writes are processed, not cached")
}

func TestUsage_Anthropic_ServedByLocalServers(t *testing.T) {
	// llama-server, vLLM and oMLX also serve /v1/messages; their extractors
	// read the Anthropic shape on that path.
	for _, upstream := range []hint.Upstream{hint.LlamaServer, hint.VLLM, hint.OMLX, hint.Unknown} {
		t.Run(string(upstream), func(t *testing.T) {
			u := parseUsageFixture(t, upstream, "/v1/messages", "anthropic/message_stream.sse")
			assert.Equal(t, 35, u.Tokens.InputTokens)
			assert.Equal(t, 42, u.Tokens.OutputTokens)
		})
	}
}
