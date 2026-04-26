// Package anthropic implements the Anthropic Messages wire format.
package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	xl "github.com/mostlygeek/llama-swap/proxy/translate"
)

type Parser struct{}
type Emitter struct{}

// --- Request ---

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	// Images / documents / thinking fall through here.
	Source json.RawMessage `json:"source,omitempty"`
	// cache_control is stripped.
}

type anthMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthRequest struct {
	Model       string          `json:"model"`
	System      json.RawMessage `json:"system,omitempty"`
	Messages    []anthMessage   `json:"messages"`
	Tools       []anthTool      `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	TopK        *int            `json:"top_k,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Stop        []string        `json:"stop_sequences,omitempty"`
}

func (Parser) ParseChatRequest(body []byte, _ xl.EndpointKind) (*xl.ChatRequest, error) {
	var req anthRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("anthropic: parse request: %w", err)
	}
	ir := &xl.ChatRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		TopK:        req.TopK,
		MaxTokens:   req.MaxTokens,
		Stop:        req.Stop,
	}
	if len(req.System) > 0 {
		sys, err := parseSystem(req.System)
		if err != nil {
			return nil, err
		}
		ir.System = sys
	}
	if len(req.ToolChoice) > 0 {
		var tc any
		_ = json.Unmarshal(req.ToolChoice, &tc)
		ir.ToolChoice = tc
	}
	for _, t := range req.Tools {
		ir.Tools = append(ir.Tools, xl.Tool{
			Name:        t.Name,
			Description: t.Description,
			Schema:      t.InputSchema,
		})
	}
	for _, m := range req.Messages {
		msg := xl.Message{Role: xl.Role(m.Role)}
		parts, err := parseContent(m.Content)
		if err != nil {
			return nil, err
		}
		msg.Content = parts
		ir.Messages = append(ir.Messages, msg)
	}
	return ir, nil
}

func parseSystem(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var arr []contentBlock
	if err := json.Unmarshal(raw, &arr); err != nil {
		return "", fmt.Errorf("anthropic: system: %w", err)
	}
	var b strings.Builder
	for _, c := range arr {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String(), nil
}

func parseContent(raw json.RawMessage) ([]xl.Part, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil, nil
		}
		return []xl.Part{{Type: xl.PartText, Text: s}}, nil
	}
	var arr []contentBlock
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("anthropic: content: %w", err)
	}
	var parts []xl.Part
	for _, c := range arr {
		switch c.Type {
		case "text":
			parts = append(parts, xl.Part{Type: xl.PartText, Text: c.Text})
		case "tool_use":
			parts = append(parts, xl.Part{
				Type:      xl.PartToolUse,
				ToolUseID: c.ID,
				ToolName:  c.Name,
				Input:     c.Input,
			})
		case "tool_result":
			out, err := flattenResult(c.Content)
			if err != nil {
				return nil, err
			}
			parts = append(parts, xl.Part{
				Type:      xl.PartToolResult,
				ToolUseID: c.ToolUseID,
				Output:    out,
				IsError:   c.IsError,
			})
		case "thinking":
			parts = append(parts, xl.Part{Type: xl.PartThinking, Text: c.Text})
		case "image", "document":
			return nil, fmt.Errorf("anthropic: images/documents not supported (501)")
		}
	}
	return parts, nil
}

func flattenResult(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var arr []contentBlock
	if err := json.Unmarshal(raw, &arr); err != nil {
		return "", fmt.Errorf("anthropic: tool_result content: %w", err)
	}
	var b strings.Builder
	for _, c := range arr {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String(), nil
}

// --- Response ---

type anthResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (Parser) ParseChatResponse(body []byte) (*xl.ChatResponse, error) {
	var r anthResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("anthropic: parse response: %w", err)
	}
	msg := xl.Message{Role: xl.RoleAssistant}
	for _, c := range r.Content {
		switch c.Type {
		case "text":
			msg.Content = append(msg.Content, xl.Part{Type: xl.PartText, Text: c.Text})
		case "tool_use":
			msg.Content = append(msg.Content, xl.Part{
				Type:      xl.PartToolUse,
				ToolUseID: c.ID,
				ToolName:  c.Name,
				Input:     c.Input,
			})
		case "thinking":
			msg.Content = append(msg.Content, xl.Part{Type: xl.PartThinking, Text: c.Text})
		}
	}
	return &xl.ChatResponse{
		ID:           r.ID,
		Model:        r.Model,
		Message:      msg,
		FinishReason: normStop(r.StopReason),
		Usage: xl.Usage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      r.Usage.InputTokens + r.Usage.OutputTokens,
		},
	}, nil
}

func normStop(s string) xl.FinishReason {
	switch s {
	case "end_turn", "stop_sequence", "":
		return xl.FinishStop
	case "max_tokens":
		return xl.FinishLength
	case "tool_use":
		return xl.FinishToolCalls
	}
	return xl.FinishStop
}

// --- Emitter ---

func (Emitter) EmitChatRequest(req *xl.ChatRequest, _ xl.EndpointKind) ([]byte, error) {
	out := map[string]any{
		"model":    req.Model,
		"messages": []any{},
	}
	if req.System != "" {
		out["system"] = req.System
	}
	var msgs []map[string]any
	for _, m := range req.Messages {
		role := string(m.Role)
		// Anthropic only accepts user/assistant. Tool role → user with tool_result.
		if role == "tool" {
			blocks := []map[string]any{}
			for _, p := range m.Content {
				if p.Type == xl.PartToolResult {
					blocks = append(blocks, toolResultBlock(p))
				}
			}
			if len(blocks) == 0 && m.ToolCallID != "" {
				blocks = append(blocks, map[string]any{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     "",
				})
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": blocks})
			continue
		}
		blocks := []map[string]any{}
		for _, p := range m.Content {
			switch p.Type {
			case xl.PartText:
				if p.Text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
				}
			case xl.PartToolUse:
				input := p.Input
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    p.ToolUseID,
					"name":  p.ToolName,
					"input": input,
				})
			case xl.PartToolResult:
				blocks = append(blocks, toolResultBlock(p))
			case xl.PartThinking:
				// Drop: can't safely re-sign; Anthropic requires signature.
			}
		}
		// OpenAI-style ToolCalls on assistant → tool_use blocks
		if role == "assistant" {
			for _, tc := range m.ToolCalls {
				input := tc.Arguments
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": input,
				})
			}
		}
		msgs = append(msgs, map[string]any{"role": role, "content": blocks})
	}
	out["messages"] = msgs

	if req.MaxTokens != nil {
		out["max_tokens"] = *req.MaxTokens
	} else {
		// Anthropic requires max_tokens; pick a safe default.
		out["max_tokens"] = 4096
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if req.TopK != nil {
		out["top_k"] = *req.TopK
	}
	if req.Stream {
		out["stream"] = true
	}
	if len(req.Stop) > 0 {
		out["stop_sequences"] = req.Stop
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.Schema,
			})
		}
		out["tools"] = tools
	}
	if req.ToolChoice != nil {
		out["tool_choice"] = req.ToolChoice
	}
	return json.Marshal(out)
}

func toolResultBlock(p xl.Part) map[string]any {
	b := map[string]any{
		"type":        "tool_result",
		"tool_use_id": p.ToolUseID,
		"content":     p.Output,
	}
	if p.IsError {
		b["is_error"] = true
	}
	return b
}

func (Emitter) EmitChatResponse(r *xl.ChatResponse) ([]byte, error) {
	var blocks []map[string]any
	for _, p := range r.Message.Content {
		switch p.Type {
		case xl.PartText:
			blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
		case xl.PartToolUse:
			input := p.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    p.ToolUseID,
				"name":  p.ToolName,
				"input": input,
			})
		}
	}
	for _, tc := range r.Message.ToolCalls {
		input := tc.Arguments
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": input,
		})
	}
	stop := "end_turn"
	switch r.FinishReason {
	case xl.FinishLength:
		stop = "max_tokens"
	case xl.FinishToolCalls:
		stop = "tool_use"
	}
	out := map[string]any{
		"id":            r.ID,
		"type":          "message",
		"role":          "assistant",
		"model":         r.Model,
		"content":       blocks,
		"stop_reason":   stop,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  r.Usage.PromptTokens,
			"output_tokens": r.Usage.CompletionTokens,
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

var (
	_ xl.Parser  = Parser{}
	_ xl.Emitter = Emitter{}
)
