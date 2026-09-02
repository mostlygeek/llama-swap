package apiconv

import (
	"encoding/json"

	"github.com/tidwall/gjson"
)

type anthropicResponse struct {
	ID           string               `json:"id"`
	Type         string               `json:"type"` // "message"
	Role         string               `json:"role"` // "assistant"
	Model        string               `json:"model"`
	Content      []anthropicRespBlock `json:"content"`
	StopReason   string               `json:"stop_reason"`
	StopSequence *string              `json:"stop_sequence"`
	Usage        anthropicUsage       `json:"usage"`
}

type anthropicRespBlock struct {
	Type  string          `json:"type"` // "text" | "tool_use"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`
}

// OpenAIToAnthropicResponse converts a buffered OpenAI chat-completion response
// body into an Anthropic /v1/messages response body. model is the name the
// client originally requested (echoed back in the response).
func OpenAIToAnthropicResponse(body []byte, model string) ([]byte, error) {
	root := gjson.ParseBytes(body)
	choice := root.Get("choices.0")
	msg := choice.Get("message")

	resp := anthropicResponse{
		ID:         anthropicMessageID(root.Get("id").String()),
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		StopReason: mapFinishReason(choice.Get("finish_reason").String()),
		Usage: anthropicUsage{
			InputTokens:          int(root.Get("usage.prompt_tokens").Int()),
			OutputTokens:         int(root.Get("usage.completion_tokens").Int()),
			CacheReadInputTokens: int(root.Get("usage.prompt_tokens_details.cached_tokens").Int()),
		},
	}

	if text := msg.Get("content").String(); text != "" {
		resp.Content = append(resp.Content, anthropicRespBlock{Type: "text", Text: text})
	}
	for _, tc := range msg.Get("tool_calls").Array() {
		input := tc.Get("function.arguments").String()
		if input == "" {
			input = "{}"
		}
		resp.Content = append(resp.Content, anthropicRespBlock{
			Type:  "tool_use",
			ID:    tc.Get("id").String(),
			Name:  tc.Get("function.name").String(),
			Input: json.RawMessage(input),
		})
	}
	// Anthropic responses always carry a content array, never null.
	if resp.Content == nil {
		resp.Content = []anthropicRespBlock{}
	}

	return json.Marshal(resp)
}

// mapFinishReason maps an OpenAI finish_reason to an Anthropic stop_reason.
func mapFinishReason(reason string) string {
	switch reason {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	case "stop":
		return "end_turn"
	case "":
		return ""
	default:
		return "end_turn"
	}
}

// anthropicMessageID returns an Anthropic-style message id, reusing the OpenAI
// id when present so behavior is deterministic.
func anthropicMessageID(openaiID string) string {
	if openaiID == "" {
		return "msg_translated"
	}
	return "msg_" + openaiID
}
