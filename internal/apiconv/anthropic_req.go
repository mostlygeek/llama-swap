package apiconv

import (
	"encoding/json"
	"fmt"
)

// AnthropicToOpenAIRequest converts an Anthropic /v1/messages request body into
// an OpenAI /v1/chat/completions request body.
func AnthropicToOpenAIRequest(body []byte) ([]byte, error) {
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid anthropic request: %w", err)
	}

	out := openaiRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
		Stream:      req.Stream,
	}
	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		out.MaxTokens = &mt
	}
	if req.Stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	// System content -> a single leading system message. Besides the top-level
	// system field, Anthropic allows "mid-conversation system messages"
	// (role:"system" entries inside messages[], a feature Claude Code uses since
	// v2.1.154). Many OpenAI-compatible backends reject a system message that is
	// not first -- e.g. the strict Qwen3 chat template raises "System message
	// must be at the beginning" -- so hoist every system block, in order, into
	// one leading system message.
	systemText := decodeAnthropicText(req.System)
	appendSystem := func(s string) {
		if s == "" {
			return
		}
		if systemText != "" {
			systemText += "\n\n"
		}
		systemText += s
	}

	var rest []openaiMessage
	for _, m := range req.Messages {
		if m.Role == "system" {
			appendSystem(decodeAnthropicText(m.Content))
			continue
		}
		msgs, err := convertAnthropicMessage(m)
		if err != nil {
			return nil, err
		}
		rest = append(rest, msgs...)
	}

	if systemText != "" {
		out.Messages = append(out.Messages, openaiMessage{Role: "system", Content: systemText})
	}
	out.Messages = append(out.Messages, rest...)

	// Tools.
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, openaiTool{
			Type: "function",
			Function: openaiToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	if tc := convertToolChoice(req.ToolChoice); tc != nil {
		out.ToolChoice = tc
	}

	// Strict chat templates (Mistral/Devstral/Magistral, Gemma, ...) raise a Jinja
	// exception -- "roles must alternate user and assistant roles except for tool
	// calls and results" -- which surfaces to the caller as an HTTP 500. Claude
	// Code's resume/tool flows trip it two ways, both handled here.
	out.Messages = normalizeForStrictTemplate(out.Messages)

	return json.Marshal(out)
}

// normalizeForStrictTemplate rewrites the message list so chat templates that
// demand strict user/assistant alternation accept it. Two transforms:
//
//  1. Merge consecutive same-role user/assistant messages (e.g. two user turns
//     produced by a resume) -- see coalesceAdjacentRoles.
//
//  2. Insert a minimal empty bridge turn when two "counted" messages of the same
//     role end up adjacent across an exempt tool exchange. These templates exclude
//     tool results AND tool-call assistant turns from the alternation check (that
//     is what "except for tool calls and results" means), so a plain user message
//     following a tool_result counts as two users in a row and is rejected -- the
//     exact shape Claude Code emits when it resumes after a tool call. An empty
//     assistant turn between the tool result and the next user message satisfies
//     the check (verified against llama.cpp's Mistral/Devstral template).
func normalizeForStrictTemplate(msgs []openaiMessage) []openaiMessage {
	merged := coalesceAdjacentRoles(msgs)

	out := make([]openaiMessage, 0, len(merged)+1)
	lastCounted := "" // role of the last non-exempt (counted) message
	for _, m := range merged {
		// Exempt from the alternation check: system, tool results, and assistant
		// turns carrying tool_calls. They never update lastCounted.
		if m.Role == "system" || m.Role == "tool" || (m.Role == "assistant" && len(m.ToolCalls) > 0) {
			out = append(out, m)
			continue
		}
		if m.Role == lastCounted {
			bridge := "assistant"
			if m.Role == "assistant" {
				bridge = "user"
			}
			out = append(out, openaiMessage{Role: bridge, Content: ""})
		}
		out = append(out, m)
		lastCounted = m.Role
	}
	return out
}

// coalesceAdjacentRoles merges consecutive user/user and assistant/assistant
// messages into one, so strict chat templates that require alternating roles do
// not reject Claude Code's message shapes. tool and system messages are left
// untouched, preserving tool_call_id linkage and system-message hoisting.
func coalesceAdjacentRoles(msgs []openaiMessage) []openaiMessage {
	out := make([]openaiMessage, 0, len(msgs))
	for _, m := range msgs {
		if len(out) > 0 && (m.Role == "user" || m.Role == "assistant") && out[len(out)-1].Role == m.Role {
			prev := &out[len(out)-1]
			prev.Content = mergeContent(prev.Content, m.Content)
			prev.ToolCalls = append(prev.ToolCalls, m.ToolCalls...)
			continue
		}
		out = append(out, m)
	}
	return out
}

// mergeContent combines two openaiMessage Content values. Two plain strings join
// with a blank line; otherwise both are normalized to parts and re-simplified, so
// a pure-text merge stays a string (what strict templates expect).
func mergeContent(a, b any) any {
	if as, aok := a.(string); aok {
		if bs, bok := b.(string); bok {
			switch {
			case as == "":
				return bs
			case bs == "":
				return as
			default:
				return as + "\n\n" + bs
			}
		}
	}
	parts := append(toParts(a), toParts(b)...)
	if len(parts) == 0 {
		return nil
	}
	return simplifyParts(parts)
}

// toParts normalizes an openaiMessage Content value (string, parts slice, or nil)
// into a parts slice for merging.
func toParts(c any) []openaiContentPart {
	switch v := c.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []openaiContentPart{{Type: "text", Text: v}}
	case []openaiContentPart:
		return v
	default:
		return nil
	}
}

// convertAnthropicMessage converts one Anthropic message into one or more
// OpenAI messages. A user message containing tool_result blocks expands into
// separate role:"tool" messages.
func convertAnthropicMessage(m anthropicMessage) ([]openaiMessage, error) {
	// Content may be a plain string.
	var asString string
	if err := json.Unmarshal(m.Content, &asString); err == nil {
		return []openaiMessage{{Role: m.Role, Content: asString}}, nil
	}

	var blocks []anthropicBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, fmt.Errorf("invalid message content for role %q: %w", m.Role, err)
	}

	var out []openaiMessage
	var parts []openaiContentPart
	var toolCalls []openaiToolCall

	flushMain := func() {
		if len(parts) == 0 && len(toolCalls) == 0 {
			return
		}
		msg := openaiMessage{Role: m.Role}
		if len(parts) > 0 {
			msg.Content = simplifyParts(parts)
		}
		if len(toolCalls) > 0 {
			msg.ToolCalls = toolCalls
		}
		out = append(out, msg)
		parts = nil
		toolCalls = nil
	}

	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, openaiContentPart{Type: "text", Text: b.Text})
		case "image":
			if b.Source != nil {
				url := fmt.Sprintf("data:%s;base64,%s", b.Source.MediaType, b.Source.Data)
				parts = append(parts, openaiContentPart{Type: "image_url", ImageURL: &openaiImageURL{URL: url}})
			}
		case "tool_use":
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, openaiToolCall{
				ID:   b.ID,
				Type: "function",
				Function: openaiToolCallFunc{
					Name:      b.Name,
					Arguments: args,
				},
			})
		case "tool_result":
			// tool_result lives in a user message but maps to a role:"tool"
			// message in OpenAI; flush any pending content first to preserve order.
			flushMain()
			out = append(out, openaiMessage{
				Role:       "tool",
				ToolCallID: b.ToolUseID,
				Content:    decodeAnthropicText(b.Content),
			})
		}
	}
	flushMain()
	return out, nil
}

// simplifyParts collapses a parts slice that is purely text into a single
// string (the common case), otherwise returns the parts slice unchanged.
func simplifyParts(parts []openaiContentPart) any {
	allText := true
	for _, p := range parts {
		if p.Type != "text" {
			allText = false
			break
		}
	}
	if allText {
		var s string
		for _, p := range parts {
			s += p.Text
		}
		return s
	}
	return parts
}

// decodeAnthropicText extracts plain text from a value that is either a JSON
// string or an array of Anthropic content blocks (concatenating text blocks).
func decodeAnthropicText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []anthropicBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var out string
		for _, b := range blocks {
			if b.Type == "text" {
				out += b.Text
			}
		}
		return out
	}
	return ""
}

// convertToolChoice maps an Anthropic tool_choice to the OpenAI equivalent.
func convertToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil
	}
	switch tc.Type {
	case "auto":
		return json.RawMessage(`"auto"`)
	case "any":
		return json.RawMessage(`"required"`)
	case "tool":
		b, err := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]string{"name": tc.Name},
		})
		if err != nil {
			return nil
		}
		return b
	default:
		return nil
	}
}
