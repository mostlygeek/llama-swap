package apiconv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIToAnthropicResponse_Text(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl-1",
		"choices":[{"index":0,"message":{"role":"assistant","content":"hello there"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":12,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":3}}
	}`)
	out, err := OpenAIToAnthropicResponse(body, "claude-req")
	require.NoError(t, err)
	r := gjson.ParseBytes(out)

	assert.Equal(t, "message", r.Get("type").String())
	assert.Equal(t, "assistant", r.Get("role").String())
	assert.Equal(t, "claude-req", r.Get("model").String())
	assert.Equal(t, "msg_chatcmpl-1", r.Get("id").String())
	assert.Equal(t, "text", r.Get("content.0.type").String())
	assert.Equal(t, "hello there", r.Get("content.0.text").String())
	assert.Equal(t, "end_turn", r.Get("stop_reason").String())
	assert.Equal(t, int64(12), r.Get("usage.input_tokens").Int())
	assert.Equal(t, int64(5), r.Get("usage.output_tokens").Int())
	assert.Equal(t, int64(3), r.Get("usage.cache_read_input_tokens").Int())
}

func TestOpenAIToAnthropicResponse_ToolUse(t *testing.T) {
	body := []byte(`{
		"id":"x",
		"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}
		]},"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":1,"completion_tokens":1}
	}`)
	out, err := OpenAIToAnthropicResponse(body, "m")
	require.NoError(t, err)
	r := gjson.ParseBytes(out)

	assert.Equal(t, "tool_use", r.Get("stop_reason").String())
	assert.Equal(t, "tool_use", r.Get("content.0.type").String())
	assert.Equal(t, "call_1", r.Get("content.0.id").String())
	assert.Equal(t, "get_weather", r.Get("content.0.name").String())
	assert.Equal(t, "SF", r.Get("content.0.input.city").String())
}

func TestOpenAIToAnthropicResponse_FinishReasonMapping(t *testing.T) {
	cases := map[string]string{
		"stop":           "end_turn",
		"length":         "max_tokens",
		"tool_calls":     "tool_use",
		"content_filter": "end_turn",
	}
	for in, want := range cases {
		body := []byte(`{"id":"x","choices":[{"message":{"content":"hi"},"finish_reason":"` + in + `"}],"usage":{}}`)
		out, err := OpenAIToAnthropicResponse(body, "m")
		require.NoError(t, err)
		assert.Equal(t, want, gjson.ParseBytes(out).Get("stop_reason").String(), "finish_reason %q", in)
	}
}

func TestOpenAIToAnthropicResponse_EmptyContentArray(t *testing.T) {
	body := []byte(`{"id":"x","choices":[{"message":{"content":""},"finish_reason":"stop"}],"usage":{}}`)
	out, err := OpenAIToAnthropicResponse(body, "m")
	require.NoError(t, err)
	r := gjson.ParseBytes(out)
	require.True(t, r.Get("content").IsArray())
	assert.Equal(t, int64(0), r.Get("content.#").Int())
}
