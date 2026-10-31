package server

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
)

func TestServer_UsageAnthropic_Messages(t *testing.T) {
	u := parseUsageJSON(t, hint.Anthropic, "/v1/messages",
		`{"type":"message","usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":2}}`)
	assert.True(t, u.Found)
	assert.Equal(t, 7, u.Tokens.InputTokens)
	assert.Equal(t, 3, u.Tokens.OutputTokens)
	assert.Equal(t, 2, u.Tokens.CachedTokens)
}

func TestServer_UsageAnthropic_MessagesStream(t *testing.T) {
	// message_start carries input and cache counts; message_delta the final
	// output count.
	u := parseUsageStream(t, hint.Anthropic, "/v1/messages",
		`{"type":"message_start","message":{"usage":{"input_tokens":25,"cache_read_input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"message_delta","usage":{"output_tokens":42}}`,
		`{"type":"message_stop"}`)
	assert.Equal(t, 25, u.Tokens.InputTokens)
	assert.Equal(t, 42, u.Tokens.OutputTokens)
	assert.Equal(t, 10, u.Tokens.CachedTokens)
}

func TestServer_UsageAnthropic_ZeroCacheIsReported(t *testing.T) {
	u := parseUsageJSON(t, hint.Anthropic, "/v1/messages",
		`{"usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":0}}`)
	assert.Equal(t, 0, u.Tokens.CachedTokens)
}

func TestServer_UsageAnthropic_ReasoningIsUnknown(t *testing.T) {
	// Thinking tokens are part of output_tokens and not broken out.
	u := parseUsageJSON(t, hint.Anthropic, "/v1/messages",
		`{"usage":{"input_tokens":7,"output_tokens":300}}`)
	assert.Equal(t, -1, u.Tokens.ReasoningTokens)
}

func TestServer_UsageAnthropic_ServedByLocalServers(t *testing.T) {
	// llama-server and vLLM also serve /v1/messages; their extractors read
	// the Anthropic shape on that path.
	for _, upstream := range []hint.Upstream{hint.LlamaServer, hint.VLLM, hint.OMLX, hint.Unknown} {
		t.Run(string(upstream), func(t *testing.T) {
			u := parseUsageStream(t, upstream, "/v1/messages",
				`{"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}`,
				`{"type":"message_delta","usage":{"output_tokens":42}}`)
			assert.Equal(t, 25, u.Tokens.InputTokens)
			assert.Equal(t, 42, u.Tokens.OutputTokens)
		})
	}
}
