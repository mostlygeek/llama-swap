// Package openai implements the OpenAI wire-format Parser/Emitter/
// StreamParser/StreamEmitter for the canonical translate IR.
package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	xl "github.com/mostlygeek/llama-swap/proxy/translate"
)

// Parser parses OpenAI /v1/chat/completions request and response bodies.
type Parser struct{}

// Emitter emits OpenAI /v1/chat/completions request and response bodies.
type Emitter struct{}

// --- Request ---

type oaiMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []oaiToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type oaiRequest struct {
	Model       string          `json:"model"`
	Messages    []oaiMessage    `json:"messages"`
	Tools       []oaiTool       `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Stop        json.RawMessage `json:"stop,omitempty"`
	Seed        *int64          `json:"seed,omitempty"`

	// Kept so round-trip emission preserves them.
	Extra map[string]json.RawMessage `json:"-"`
}

var knownReqKeys = map[string]bool{
	"model": true, "messages": true, "tools": true, "tool_choice": true,
	"stream": true, "temperature": true, "top_p": true, "max_tokens": true,
	"stop": true, "seed": true,
}

// ParseChatRequest turns an OpenAI request body into IR.
func (Parser) ParseChatRequest(body []byte, _ xl.EndpointKind) (*xl.ChatRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("openai: parse request: %w", err)
	}
	var req oaiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("openai: parse request: %w", err)
	}
	extra := map[string]any{}
	for k, v := range raw {
		if !knownReqKeys[k] {
			var decoded any
			_ = json.Unmarshal(v, &decoded)
			extra[k] = decoded
		}
	}

	ir := &xl.ChatRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
		Seed:        req.Seed,
	}
	if len(extra) > 0 {
		ir.Extra = extra
	}
	if len(req.Stop) > 0 {
		ir.Stop = parseStop(req.Stop)
	}
	if len(req.ToolChoice) > 0 {
		var tc any
		_ = json.Unmarshal(req.ToolChoice, &tc)
		ir.ToolChoice = tc
	}
	for _, t := range req.Tools {
		ir.Tools = append(ir.Tools, xl.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Schema:      t.Function.Parameters,
		})
	}
	for _, m := range req.Messages {
		msg := xl.Message{Role: xl.Role(m.Role), Name: m.Name, ToolCallID: m.ToolCallID}
		if m.Role == "system" && ir.System == "" && len(m.ToolCalls) == 0 {
			if s, ok := plainText(m.Content); ok {
				ir.System = s
				continue
			}
		}
		if len(m.Content) > 0 {
			if parts, err := parseContent(m.Content); err == nil {
				msg.Content = parts
			} else {
				return nil, err
			}
		}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, xl.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		if m.Role == "tool" && len(msg.Content) > 0 {
			// Fold into canonical tool_result part
			if s, ok := plainText(m.Content); ok {
				msg.Content = []xl.Part{{
					Type:      xl.PartToolResult,
					ToolUseID: m.ToolCallID,
					Output:    s,
				}}
			}
		}
		ir.Messages = append(ir.Messages, msg)
	}
	return ir, nil
}

func parseStop(raw json.RawMessage) []string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	return nil
}

func plainText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	return "", false
}

func parseContent(raw json.RawMessage) ([]xl.Part, error) {
	if s, ok := plainText(raw); ok {
		if s == "" {
			return nil, nil
		}
		return []xl.Part{{Type: xl.PartText, Text: s}}, nil
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("openai: content: %w", err)
	}
	var parts []xl.Part
	for _, item := range arr {
		var t string
		_ = json.Unmarshal(item["type"], &t)
		switch t {
		case "text":
			var txt string
			_ = json.Unmarshal(item["text"], &txt)
			parts = append(parts, xl.Part{Type: xl.PartText, Text: txt})
		case "image_url", "input_audio", "image":
			return nil, fmt.Errorf("openai: images/audio not supported")
		default:
			var txt string
			if _, ok := item["text"]; ok {
				_ = json.Unmarshal(item["text"], &txt)
				parts = append(parts, xl.Part{Type: xl.PartText, Text: txt})
			}
		}
	}
	return parts, nil
}

// --- Response ---

type oaiResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int        `json:"index"`
		Message      oaiMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
		Logprobs     any        `json:"logprobs,omitempty"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (Parser) ParseChatResponse(body []byte) (*xl.ChatResponse, error) {
	var r oaiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("openai: parse response: %w", err)
	}
	if len(r.Choices) == 0 {
		return nil, fmt.Errorf("openai: response has no choices")
	}
	c := r.Choices[0]
	msg := xl.Message{Role: xl.Role(c.Message.Role)}
	if parts, err := parseContent(c.Message.Content); err == nil {
		msg.Content = parts
	}
	for _, tc := range c.Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, xl.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return &xl.ChatResponse{
		ID:           r.ID,
		Model:        r.Model,
		Created:      r.Created,
		Message:      msg,
		FinishReason: normalizeFinish(c.FinishReason),
		Usage: xl.Usage{
			PromptTokens:     r.Usage.PromptTokens,
			CompletionTokens: r.Usage.CompletionTokens,
			TotalTokens:      r.Usage.TotalTokens,
		},
	}, nil
}

func normalizeFinish(s string) xl.FinishReason {
	switch s {
	case "stop", "":
		return xl.FinishStop
	case "length":
		return xl.FinishLength
	case "tool_calls", "function_call":
		return xl.FinishToolCalls
	}
	return xl.FinishStop
}

// --- Emitter ---

func (Emitter) EmitChatRequest(req *xl.ChatRequest, _ xl.EndpointKind) ([]byte, error) {
	out := map[string]any{
		"model": req.Model,
	}
	var msgs []map[string]any
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		om := map[string]any{"role": string(m.Role)}
		if m.Name != "" {
			om["name"] = m.Name
		}
		// tool-role messages carry tool_call_id and string output
		if m.Role == xl.RoleTool {
			var outStr string
			var toolID string
			for _, p := range m.Content {
				if p.Type == xl.PartToolResult {
					outStr = p.Output
					toolID = p.ToolUseID
				}
			}
			if m.ToolCallID != "" {
				toolID = m.ToolCallID
			}
			om["tool_call_id"] = toolID
			om["content"] = outStr
			msgs = append(msgs, om)
			continue
		}
		// tool_result parts at user-role (Anthropic style) → separate tool messages
		hasToolResult := false
		for _, p := range m.Content {
			if p.Type == xl.PartToolResult {
				hasToolResult = true
				break
			}
		}
		if hasToolResult && m.Role == xl.RoleUser {
			for _, p := range m.Content {
				if p.Type == xl.PartToolResult {
					msgs = append(msgs, map[string]any{
						"role":         "tool",
						"tool_call_id": p.ToolUseID,
						"content":      p.Output,
					})
				} else if p.Type == xl.PartText && p.Text != "" {
					msgs = append(msgs, map[string]any{"role": "user", "content": p.Text})
				}
			}
			continue
		}
		// Assistant with tool_use parts
		if m.Role == xl.RoleAssistant {
			var text strings.Builder
			var calls []map[string]any
			for _, p := range m.Content {
				switch p.Type {
				case xl.PartText:
					text.WriteString(p.Text)
				case xl.PartToolUse:
					args := p.Input
					if len(args) == 0 {
						args = json.RawMessage(`{}`)
					}
					argsStr, _ := json.Marshal(string(args))
					// OpenAI expects arguments as a JSON-encoded string
					calls = append(calls, map[string]any{
						"id":   p.ToolUseID,
						"type": "function",
						"function": map[string]any{
							"name":      p.ToolName,
							"arguments": json.RawMessage(argsStr),
						},
					})
				}
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				argsStr, _ := json.Marshal(string(args))
				calls = append(calls, map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": json.RawMessage(argsStr),
					},
				})
			}
			if text.Len() > 0 {
				om["content"] = text.String()
			} else {
				om["content"] = nil
			}
			if len(calls) > 0 {
				om["tool_calls"] = calls
			}
			msgs = append(msgs, om)
			continue
		}
		// Default: concatenate text parts
		var text strings.Builder
		for _, p := range m.Content {
			if p.Type == xl.PartText {
				text.WriteString(p.Text)
			}
		}
		om["content"] = text.String()
		msgs = append(msgs, om)
	}
	out["messages"] = msgs

	if req.Stream {
		out["stream"] = true
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		out["max_tokens"] = *req.MaxTokens
	}
	if req.Seed != nil {
		out["seed"] = *req.Seed
	}
	if len(req.Stop) > 0 {
		if len(req.Stop) == 1 {
			out["stop"] = req.Stop[0]
		} else {
			out["stop"] = req.Stop
		}
	}
	if req.ToolChoice != nil {
		out["tool_choice"] = req.ToolChoice
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Schema,
				},
			})
		}
		out["tools"] = tools
	}
	for k, v := range req.Extra {
		if _, clash := out[k]; !clash {
			out[k] = v
		}
	}
	return json.Marshal(out)
}

func (Emitter) EmitChatResponse(r *xl.ChatResponse) ([]byte, error) {
	var content any
	var calls []map[string]any
	var textBuf strings.Builder
	for _, p := range r.Message.Content {
		switch p.Type {
		case xl.PartText:
			textBuf.WriteString(p.Text)
		case xl.PartToolUse:
			args := p.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			argsStr, _ := json.Marshal(string(args))
			calls = append(calls, map[string]any{
				"id":   p.ToolUseID,
				"type": "function",
				"function": map[string]any{
					"name":      p.ToolName,
					"arguments": json.RawMessage(argsStr),
				},
			})
		}
	}
	for _, tc := range r.Message.ToolCalls {
		args := tc.Arguments
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		argsStr, _ := json.Marshal(string(args))
		calls = append(calls, map[string]any{
			"id":   tc.ID,
			"type": "function",
			"function": map[string]any{
				"name":      tc.Name,
				"arguments": json.RawMessage(argsStr),
			},
		})
	}
	if textBuf.Len() > 0 {
		content = textBuf.String()
	}

	msg := map[string]any{"role": string(r.Message.Role)}
	if content != nil {
		msg["content"] = content
	} else {
		msg["content"] = nil
	}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}

	finish := string(r.FinishReason)
	if finish == "" {
		finish = "stop"
	}
	out := map[string]any{
		"id":      r.ID,
		"object":  "chat.completion",
		"created": r.Created,
		"model":   r.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": finish,
		}},
		"usage": map[string]any{
			"prompt_tokens":     r.Usage.PromptTokens,
			"completion_tokens": r.Usage.CompletionTokens,
			"total_tokens":      r.Usage.TotalTokens,
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Ensure interface compliance.
var (
	_ xl.Parser  = Parser{}
	_ xl.Emitter = Emitter{}
)

// compile-time unused import guards
var _ = io.Discard
