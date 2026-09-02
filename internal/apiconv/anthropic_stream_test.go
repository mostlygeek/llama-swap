package apiconv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// newTestWriter returns an httptest recorder both as the http.ResponseWriter the
// stream writer wraps and for inspecting what was written.
func newTestWriter() (http.ResponseWriter, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	return rec, rec
}

// sseEvent is one parsed "event:/data:" frame from an Anthropic SSE stream.
type sseEvent struct {
	event string
	data  gjson.Result
}

func parseAnthropicSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	var events []sseEvent
	for _, frame := range strings.Split(body, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		var ev sseEvent
		for _, line := range strings.Split(frame, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "event:"):
				ev.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				ev.data = gjson.Parse(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		events = append(events, ev)
	}
	return events
}

func eventTypes(events []sseEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.event
	}
	return out
}

func TestAnthropicStreamWriter_BasicText(t *testing.T) {
	gw, rec := newTestWriter()
	w := NewAnthropicStreamWriter(gw, "claude-req")
	w.Header().Set("Content-Type", "text/event-stream")

	sse := strings.Join([]string{
		`data: {"id":"chatcmpl-x","choices":[{"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"content":" there"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte(sse))
	require.NoError(t, err)
	w.Finalize()

	events := parseAnthropicSSE(t, rec.Body.String())
	assert.Equal(t, []string{
		"message_start", "content_block_start",
		"content_block_delta", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop",
	}, eventTypes(events))

	assert.Equal(t, "msg_chatcmpl-x", events[0].data.Get("message.id").String())
	assert.Equal(t, "claude-req", events[0].data.Get("message.model").String())
	assert.Equal(t, "text", events[1].data.Get("content_block.type").String())
	assert.Equal(t, "Hi", events[2].data.Get("delta.text").String())
	assert.Equal(t, " there", events[3].data.Get("delta.text").String())

	md := events[5].data
	assert.Equal(t, "end_turn", md.Get("delta.stop_reason").String())
	assert.Equal(t, int64(2), md.Get("usage.output_tokens").Int())
	assert.Equal(t, int64(5), md.Get("usage.input_tokens").Int())
}

func TestAnthropicStreamWriter_ChunkedWrites(t *testing.T) {
	gw, rec := newTestWriter()
	w := NewAnthropicStreamWriter(gw, "m")
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	// Feed the stream split across arbitrary byte boundaries, including mid-frame.
	full := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"AB"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"CD"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	for i := 0; i < len(full); i += 7 {
		end := i + 7
		if end > len(full) {
			end = len(full)
		}
		_, err := w.Write([]byte(full[i:end]))
		require.NoError(t, err)
	}
	w.Finalize()

	events := parseAnthropicSSE(t, rec.Body.String())
	// Reconstruct the streamed text from text_delta events.
	var text string
	for _, e := range events {
		if e.event == "content_block_delta" && e.data.Get("delta.type").String() == "text_delta" {
			text += e.data.Get("delta.text").String()
		}
	}
	assert.Equal(t, "ABCD", text)
	assert.Equal(t, "message_stop", events[len(events)-1].event)
}

func TestAnthropicStreamWriter_ToolCall(t *testing.T) {
	gw, rec := newTestWriter()
	w := NewAnthropicStreamWriter(gw, "m")
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"SF\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	_, err := w.Write([]byte(sse))
	require.NoError(t, err)
	w.Finalize()

	events := parseAnthropicSSE(t, rec.Body.String())

	var start sseEvent
	var partial string
	for _, e := range events {
		if e.event == "content_block_start" && e.data.Get("content_block.type").String() == "tool_use" {
			start = e
		}
		if e.event == "content_block_delta" && e.data.Get("delta.type").String() == "input_json_delta" {
			partial += e.data.Get("delta.partial_json").String()
		}
	}
	assert.Equal(t, "call_1", start.data.Get("content_block.id").String())
	assert.Equal(t, "get_weather", start.data.Get("content_block.name").String())
	assert.Equal(t, `{"city":"SF"}`, partial)

	// stop_reason should reflect tool use
	last := events[len(events)-2] // message_delta is second to last
	assert.Equal(t, "message_delta", last.event)
	assert.Equal(t, "tool_use", last.data.Get("delta.stop_reason").String())
}

func TestAnthropicStreamWriter_PassThroughOnError(t *testing.T) {
	gw, rec := newTestWriter()
	w := NewAnthropicStreamWriter(gw, "m")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)

	errBody := `{"error":{"message":"boom"}}`
	_, err := w.Write([]byte(errBody))
	require.NoError(t, err)
	w.Finalize()

	assert.Equal(t, errBody, rec.Body.String())
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAnthropicStreamWriter_FinalizeWithoutDONE(t *testing.T) {
	gw, rec := newTestWriter()
	w := NewAnthropicStreamWriter(gw, "m")
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	// No [DONE] sentinel; Finalize must still close the message.
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"
	_, err := w.Write([]byte(sse))
	require.NoError(t, err)
	w.Finalize()

	events := parseAnthropicSSE(t, rec.Body.String())
	types := eventTypes(events)
	assert.Contains(t, types, "message_stop")
	assert.Equal(t, "message_stop", types[len(types)-1])
}
