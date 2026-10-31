package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helpers shared by the <upstream>_test.go files.

// parseUsageFixture runs a recorded response from testdata through the
// extractor for u. A .sse file is sent as an event stream, anything else as
// a JSON body. Fixtures live in one directory per upstream, e.g.
// testdata/vllm/chat_completion_stream.sse.
func parseUsageFixture(t *testing.T, u hint.Upstream, path, file string) Usage {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", file))
	require.NoError(t, err)
	contentType := "application/json"
	if strings.HasSuffix(file, ".sse") {
		contentType = "text/event-stream"
	}
	usage, err := Extract(u, path, contentType, body, time.Now())
	require.NoError(t, err)
	return usage
}

// parseUsageJSON runs a non-streaming body through the extractor for u.
func parseUsageJSON(t *testing.T, u hint.Upstream, path, body string) Usage {
	t.Helper()
	usage, err := Extract(u, path, "application/json", []byte(body), time.Now())
	require.NoError(t, err)
	return usage
}

// parseUsageStream sends each event as an SSE data: line, followed by
// [DONE], through the extractor for u.
func parseUsageStream(t *testing.T, u hint.Upstream, path string, events ...string) Usage {
	t.Helper()
	usage, err := Extract(u, path, "text/event-stream", []byte(sseBody(events...)), time.Now())
	require.NoError(t, err)
	return usage
}

func sseBody(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// contentChunk is a streamed chat chunk with no usage, the shape of every
// chunk before the last.
const contentChunk = `{"choices":[{"delta":{"content":"hi"}}]}`

// Tests of the dispatch in usage.go. Each extractor's own parsing is tested
// in its <upstream>_test.go file.

func TestUsage_ExtractorFor(t *testing.T) {
	tests := []struct {
		upstream hint.Upstream
		want     usageExtractor
	}{
		{hint.LlamaServer, llamaServerExtractor{}},
		{hint.VLLM, vllmExtractor{}},
		{hint.TabbyAPI, tabbyAPIExtractor{}},
		{hint.OMLX, omlxExtractor{}},
		{hint.Kyojin, kyojinExtractor{}},
		{hint.Radiance, radianceExtractor{}},
		{hint.OpenAI, apiExtractor{}},
		{hint.Anthropic, apiExtractor{}},
		{hint.OpenRouter, apiExtractor{}},
		{hint.OpenAICompatible, autoExtractor{}},
		{hint.Halogen, halogenExtractor{}},
		{hint.LlamaSwap, autoExtractor{}},
		{hint.Unknown, autoExtractor{}},
	}
	for _, tt := range tests {
		assert.IsType(t, tt.want, extractorFor(tt.upstream), "upstream %q", tt.upstream)
	}
}

func TestUsage_APIReaderForPath(t *testing.T) {
	anthropic := `{"usage":{"input_tokens":5,"output_tokens":2}}`
	chat := `{"usage":{"prompt_tokens":5,"completion_tokens":2}}`
	tests := []struct {
		path string
		body string
	}{
		{"/v1/messages", anthropic},
		{"/v/messages", anthropic},
		{"/v1/chat/completions", chat},
		{"/v1/embeddings", chat},
	}
	for _, tt := range tests {
		u := parseUsageJSON(t, hint.OpenAICompatible, tt.path, tt.body)
		assert.Equal(t, 5, u.Tokens.InputTokens, tt.path)
		assert.Equal(t, 2, u.Tokens.OutputTokens, tt.path)
	}
}

func TestUsage_StreamWithoutUsageIsAnError(t *testing.T) {
	_, err := Extract(hint.Unknown, "/v1/chat/completions", "text/event-stream", []byte(sseBody(contentChunk)), time.Now())
	assert.ErrorContains(t, err, "no valid JSON data found in stream")
}

func TestUsage_InvalidJSONIsAnError(t *testing.T) {
	_, err := Extract(hint.Unknown, "/v1/chat/completions", "application/json", []byte("not json"), time.Now())
	assert.ErrorContains(t, err, "invalid JSON in response body")
}

func TestUsage_JSONWithoutUsageIsNotFound(t *testing.T) {
	u := parseUsageJSON(t, hint.Unknown, "/v1/chat/completions", `{"error":"boom"}`)
	assert.False(t, u.Found)
}

func TestUsage_UnrecognizedUsageObjectIsFound(t *testing.T) {
	u := parseUsageJSON(t, hint.Unknown, "/v1/images/generations", `{"usage":{"images":1}}`)
	assert.True(t, u.Found, "a usage object counts as reported even with unknown fields")
}

func TestUsage_SSESkipsNonDataLines(t *testing.T) {
	body := "event: message\n: comment\ndata:\ndata: {bad json}\n" +
		"data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4}}\n\ndata: [DONE]\n"
	u, err := Extract(hint.Unknown, "/v1/chat/completions", "text/event-stream", []byte(body), time.Now())
	require.NoError(t, err)
	assert.Equal(t, 3, u.Tokens.InputTokens)
	assert.Equal(t, 4, u.Tokens.OutputTokens)
}
