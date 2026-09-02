package apiconv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAnthropicToOpenAIRequest_BasicTextAndSystem(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"max_tokens": 100,
		"system": "be brief",
		"temperature": 0.5,
		"stop_sequences": ["END"],
		"messages": [
			{"role": "user", "content": "hello"}
		]
	}`)

	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)

	assert.Equal(t, "m", r.Get("model").String())
	assert.Equal(t, int64(100), r.Get("max_tokens").Int())
	assert.InDelta(t, 0.5, r.Get("temperature").Float(), 0.0001)
	assert.Equal(t, "END", r.Get("stop.0").String())

	assert.Equal(t, "system", r.Get("messages.0.role").String())
	assert.Equal(t, "be brief", r.Get("messages.0.content").String())
	assert.Equal(t, "user", r.Get("messages.1.role").String())
	assert.Equal(t, "hello", r.Get("messages.1.content").String())
}

func TestAnthropicToOpenAIRequest_StreamSetsIncludeUsage(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)
	assert.True(t, r.Get("stream").Bool())
	assert.True(t, r.Get("stream_options.include_usage").Bool())
}

func TestAnthropicToOpenAIRequest_SystemAsBlocks(t *testing.T) {
	body := []byte(`{
		"model":"m","max_tokens":10,
		"system":[{"type":"text","text":"part1 "},{"type":"text","text":"part2"}],
		"messages":[{"role":"user","content":"hi"}]
	}`)
	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)
	assert.Equal(t, "system", r.Get("messages.0.role").String())
	assert.Equal(t, "part1 part2", r.Get("messages.0.content").String())
}

func TestAnthropicToOpenAIRequest_MidConversationSystemHoisted(t *testing.T) {
	// Claude Code (v2.1.154+) emits role:"system" messages inside messages[].
	// Strict backends (e.g. Qwen3) require exactly one system message, first, so
	// all system content must be merged into a single leading system message.
	body := []byte(`{
		"model":"m","max_tokens":10,
		"system":"top system",
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"system","content":"mid-stream note"},
			{"role":"assistant","content":"ok"},
			{"role":"system","content":[{"type":"text","text":"another note"}]},
			{"role":"user","content":"bye"}
		]
	}`)
	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)

	// Exactly one system message, at index 0, with all system text merged in order.
	assert.Equal(t, "system", r.Get("messages.0.role").String())
	assert.Equal(t, "top system\n\nmid-stream note\n\nanother note", r.Get("messages.0.content").String())
	r.Get("messages").ForEach(func(i, m gjson.Result) bool {
		if i.Int() > 0 {
			assert.NotEqual(t, "system", m.Get("role").String(), "no system message past index 0")
		}
		return true
	})
	// Non-system messages preserve their order after the system message.
	assert.Equal(t, "user", r.Get("messages.1.role").String())
	assert.Equal(t, "hi", r.Get("messages.1.content").String())
	assert.Equal(t, "assistant", r.Get("messages.2.role").String())
	assert.Equal(t, "user", r.Get("messages.3.role").String())
	assert.Equal(t, "bye", r.Get("messages.3.content").String())
}

func TestAnthropicToOpenAIRequest_ImageBlock(t *testing.T) {
	body := []byte(`{
		"model":"m","max_tokens":10,
		"messages":[{"role":"user","content":[
			{"type":"text","text":"what is this"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}
		]}]
	}`)
	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)
	parts := r.Get("messages.0.content")
	require.True(t, parts.IsArray())
	assert.Equal(t, "text", parts.Get("0.type").String())
	assert.Equal(t, "image_url", parts.Get("1.type").String())
	assert.Equal(t, "data:image/png;base64,AAAA", parts.Get("1.image_url.url").String())
}

func TestAnthropicToOpenAIRequest_Tools(t *testing.T) {
	body := []byte(`{
		"model":"m","max_tokens":10,
		"tools":[{"name":"get_weather","description":"gets weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}],
		"tool_choice":{"type":"auto"},
		"messages":[{"role":"user","content":"weather?"}]
	}`)
	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)
	assert.Equal(t, "function", r.Get("tools.0.type").String())
	assert.Equal(t, "get_weather", r.Get("tools.0.function.name").String())
	assert.Equal(t, "object", r.Get("tools.0.function.parameters.type").String())
	assert.Equal(t, "auto", r.Get("tool_choice").String())
}

func TestAnthropicToOpenAIRequest_ToolUseAndResult(t *testing.T) {
	body := []byte(`{
		"model":"m","max_tokens":10,
		"messages":[
			{"role":"assistant","content":[
				{"type":"text","text":"let me check"},
				{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"sunny"}
			]}
		]
	}`)
	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)

	// assistant message with tool_calls
	assert.Equal(t, "assistant", r.Get("messages.0.role").String())
	assert.Equal(t, "let me check", r.Get("messages.0.content").String())
	assert.Equal(t, "toolu_1", r.Get("messages.0.tool_calls.0.id").String())
	assert.Equal(t, "get_weather", r.Get("messages.0.tool_calls.0.function.name").String())
	assert.Equal(t, "SF", gjson.Parse(r.Get("messages.0.tool_calls.0.function.arguments").String()).Get("city").String())

	// tool result becomes role:tool message
	assert.Equal(t, "tool", r.Get("messages.1.role").String())
	assert.Equal(t, "toolu_1", r.Get("messages.1.tool_call_id").String())
	assert.Equal(t, "sunny", r.Get("messages.1.content").String())
}

func TestAnthropicToOpenAIRequest_ConsecutiveUserMerged(t *testing.T) {
	// Claude Code's resume flow can emit two user messages back to back. Strict
	// templates reject that; coalesce them into one so roles alternate.
	body := []byte(`{
		"model":"m","max_tokens":10,
		"system":"sys",
		"messages":[
			{"role":"user","content":"first"},
			{"role":"user","content":"second"},
			{"role":"assistant","content":"ok"}
		]
	}`)
	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)

	// system, merged user, assistant -- three messages total.
	assert.Equal(t, 3, len(r.Get("messages").Array()))
	assert.Equal(t, "system", r.Get("messages.0.role").String())
	assert.Equal(t, "user", r.Get("messages.1.role").String())
	assert.Equal(t, "first\n\nsecond", r.Get("messages.1.content").String())
	assert.Equal(t, "assistant", r.Get("messages.2.role").String())
	assert.Equal(t, "ok", r.Get("messages.2.content").String())
}

func TestAnthropicToOpenAIRequest_ConsecutiveAssistantMerged(t *testing.T) {
	// An assistant text turn followed by an assistant tool_use turn must collapse
	// into one message carrying both content and tool_calls.
	body := []byte(`{
		"model":"m","max_tokens":10,
		"messages":[
			{"role":"assistant","content":"thinking"},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
			]}
		]
	}`)
	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)

	assert.Equal(t, 1, len(r.Get("messages").Array()))
	assert.Equal(t, "assistant", r.Get("messages.0.role").String())
	assert.Equal(t, "thinking", r.Get("messages.0.content").String())
	assert.Equal(t, "toolu_1", r.Get("messages.0.tool_calls.0.id").String())
	assert.Equal(t, "get_weather", r.Get("messages.0.tool_calls.0.function.name").String())
}

func TestAnthropicToOpenAIRequest_ToolResultNotMerged(t *testing.T) {
	// A normal assistant(tool_calls) -> tool_result -> user sequence must NOT be
	// merged: the role:tool message stays separate with its tool_call_id intact.
	body := []byte(`{
		"model":"m","max_tokens":10,
		"messages":[
			{"role":"assistant","content":[
				{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"sunny"}
			]},
			{"role":"user","content":"thanks"}
		]
	}`)
	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)

	// assistant, tool, user -- the tool message keeps roles non-adjacent so the
	// two user-origin messages are not merged across it.
	assert.Equal(t, 3, len(r.Get("messages").Array()))
	assert.Equal(t, "assistant", r.Get("messages.0.role").String())
	assert.Equal(t, "tool", r.Get("messages.1.role").String())
	assert.Equal(t, "toolu_1", r.Get("messages.1.tool_call_id").String())
	assert.Equal(t, "sunny", r.Get("messages.1.content").String())
	assert.Equal(t, "user", r.Get("messages.2.role").String())
	assert.Equal(t, "thanks", r.Get("messages.2.content").String())
}

func TestAnthropicToOpenAIRequest_ToolThenUserBridged(t *testing.T) {
	// The real resume shape Claude Code emits: user -> assistant(tool_call) ->
	// tool_result -> user continuation. Strict templates (Mistral/Devstral) exempt
	// the tool_call and tool_result turns from their alternation check, so the two
	// user turns read as consecutive and are rejected. An empty assistant bridge
	// must be inserted between the tool result and the trailing user message.
	body := []byte(`{
		"model":"m","max_tokens":10,
		"messages":[
			{"role":"user","content":"what time is it"},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"toolu_1","name":"get_time","input":{}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"10:00"}
			]},
			{"role":"user","content":"continue"}
		]
	}`)
	out, err := AnthropicToOpenAIRequest(body)
	require.NoError(t, err)
	r := gjson.ParseBytes(out)

	// user, assistant(tool_call), tool, assistant(""), user -- five messages with
	// the empty bridge separating the tool result from the user continuation.
	msgs := r.Get("messages").Array()
	require.Equal(t, 5, len(msgs))
	assert.Equal(t, "user", msgs[0].Get("role").String())
	assert.Equal(t, "assistant", msgs[1].Get("role").String())
	assert.Equal(t, "toolu_1", msgs[1].Get("tool_calls.0.id").String())
	assert.Equal(t, "tool", msgs[2].Get("role").String())
	assert.Equal(t, "10:00", msgs[2].Get("content").String())
	assert.Equal(t, "assistant", msgs[3].Get("role").String())
	assert.Equal(t, "", msgs[3].Get("content").String())
	assert.Empty(t, msgs[3].Get("tool_calls").Array())
	assert.Equal(t, "user", msgs[4].Get("role").String())
	assert.Equal(t, "continue", msgs[4].Get("content").String())
}

func TestAnthropicToOpenAIRequest_InvalidJSON(t *testing.T) {
	_, err := AnthropicToOpenAIRequest([]byte(`{not json`))
	require.Error(t, err)
}
