// Package ollama implements the Ollama /api/chat and /api/generate wire format.
package ollama

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	xl "github.com/mostlygeek/llama-swap/proxy/translate"
)

// timeNow is a package-level hook so streaming tests can deterministically
// produce `created_at` timestamps.
var timeNow = func() string { return time.Now().UTC().Format(time.RFC3339Nano) }

type Parser struct{}
type Emitter struct{}

// --- Request ---

type olToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type olMessage struct {
	Role      string       `json:"role"`
	Content   string       `json:"content"`
	ToolCalls []olToolCall `json:"tool_calls,omitempty"`
	// For tool role Ollama has no tool_use_id; content is the result.
}

type olTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type olChatRequest struct {
	Model     string                     `json:"model"`
	Messages  []olMessage                `json:"messages"`
	Tools     []olTool                   `json:"tools,omitempty"`
	Stream    *bool                      `json:"stream,omitempty"`
	Format    json.RawMessage            `json:"format,omitempty"`
	KeepAlive json.RawMessage            `json:"keep_alive,omitempty"`
	Options   map[string]json.RawMessage `json:"options,omitempty"`
	System    string                     `json:"system,omitempty"`
}

type olGenerateRequest struct {
	Model     string                     `json:"model"`
	Prompt    string                     `json:"prompt"`
	System    string                     `json:"system,omitempty"`
	Stream    *bool                      `json:"stream,omitempty"`
	Format    json.RawMessage            `json:"format,omitempty"`
	KeepAlive json.RawMessage            `json:"keep_alive,omitempty"`
	Options   map[string]json.RawMessage `json:"options,omitempty"`
}

func (Parser) ParseChatRequest(body []byte, kind xl.EndpointKind) (*xl.ChatRequest, error) {
	if kind == xl.KindGenerate {
		var g olGenerateRequest
		if err := json.Unmarshal(body, &g); err != nil {
			return nil, fmt.Errorf("ollama: parse generate: %w", err)
		}
		ir := &xl.ChatRequest{Model: g.Model, System: g.System}
		if g.Stream != nil {
			ir.Stream = *g.Stream
		} else {
			ir.Stream = true // Ollama default is stream
		}
		if g.Prompt != "" {
			ir.Messages = []xl.Message{{
				Role:    xl.RoleUser,
				Content: []xl.Part{{Type: xl.PartText, Text: g.Prompt}},
			}}
		}
		applyOptions(ir, g.Options)
		return ir, nil
	}
	var r olChatRequest
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("ollama: parse chat: %w", err)
	}
	ir := &xl.ChatRequest{Model: r.Model, System: r.System}
	if r.Stream != nil {
		ir.Stream = *r.Stream
	} else {
		ir.Stream = true
	}
	applyOptions(ir, r.Options)
	for _, t := range r.Tools {
		ir.Tools = append(ir.Tools, xl.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Schema:      t.Function.Parameters,
		})
	}
	toolIdx := 0
	for i, m := range r.Messages {
		msg := xl.Message{Role: xl.Role(m.Role)}
		if m.Role == "system" && ir.System == "" && i == 0 && len(m.ToolCalls) == 0 {
			ir.System = m.Content
			continue
		}
		if m.Role == "tool" {
			// positional match to the preceding assistant tool_call
			id := ""
			toolName := ""
			if len(ir.Messages) > 0 {
				last := &ir.Messages[len(ir.Messages)-1]
				if last.Role == xl.RoleAssistant && toolIdx < len(last.ToolCalls) {
					id = last.ToolCalls[toolIdx].ID
					toolName = last.ToolCalls[toolIdx].Name
					toolIdx++
				}
			}
			_ = toolName
			msg.Role = xl.RoleTool
			msg.ToolCallID = id
			msg.Content = []xl.Part{{
				Type:      xl.PartToolResult,
				ToolUseID: id,
				Output:    m.Content,
			}}
			ir.Messages = append(ir.Messages, msg)
			continue
		}
		if m.Content != "" {
			msg.Content = []xl.Part{{Type: xl.PartText, Text: m.Content}}
		}
		for idx, tc := range m.ToolCalls {
			id := fmt.Sprintf("call_ol_%s_%d", tc.Function.Name, idx)
			msg.ToolCalls = append(msg.ToolCalls, xl.ToolCall{
				ID:        id,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		toolIdx = 0
		ir.Messages = append(ir.Messages, msg)
	}
	return ir, nil
}

func applyOptions(ir *xl.ChatRequest, opts map[string]json.RawMessage) {
	if v, ok := opts["temperature"]; ok {
		var f float64
		if json.Unmarshal(v, &f) == nil {
			ir.Temperature = &f
		}
	}
	if v, ok := opts["top_p"]; ok {
		var f float64
		if json.Unmarshal(v, &f) == nil {
			ir.TopP = &f
		}
	}
	if v, ok := opts["top_k"]; ok {
		var i int
		if json.Unmarshal(v, &i) == nil {
			ir.TopK = &i
		}
	}
	if v, ok := opts["num_predict"]; ok {
		var i int
		if json.Unmarshal(v, &i) == nil {
			ir.MaxTokens = &i
		}
	}
	if v, ok := opts["seed"]; ok {
		var i int64
		if json.Unmarshal(v, &i) == nil {
			ir.Seed = &i
		}
	}
	if v, ok := opts["stop"]; ok {
		var arr []string
		if json.Unmarshal(v, &arr) == nil {
			ir.Stop = arr
		} else {
			var s string
			if json.Unmarshal(v, &s) == nil {
				ir.Stop = []string{s}
			}
		}
	}
}

// --- Response ---

type olChatResponse struct {
	Model           string    `json:"model"`
	CreatedAt       time.Time `json:"created_at"`
	Message         olMessage `json:"message"`
	Done            bool      `json:"done"`
	DoneReason      string    `json:"done_reason,omitempty"`
	PromptEvalCount int       `json:"prompt_eval_count,omitempty"`
	EvalCount       int       `json:"eval_count,omitempty"`
}

func (Parser) ParseChatResponse(body []byte) (*xl.ChatResponse, error) {
	var r olChatResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("ollama: parse response: %w", err)
	}
	msg := xl.Message{Role: xl.RoleAssistant}
	if r.Message.Content != "" {
		msg.Content = append(msg.Content, xl.Part{Type: xl.PartText, Text: r.Message.Content})
	}
	for idx, tc := range r.Message.ToolCalls {
		id := fmt.Sprintf("call_ol_%s_%d", tc.Function.Name, idx)
		msg.ToolCalls = append(msg.ToolCalls, xl.ToolCall{
			ID:        id,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	finish := xl.FinishStop
	switch r.DoneReason {
	case "length":
		finish = xl.FinishLength
	case "tool_calls":
		finish = xl.FinishToolCalls
	}
	if len(msg.ToolCalls) > 0 && finish == xl.FinishStop {
		finish = xl.FinishToolCalls
	}
	return &xl.ChatResponse{
		ID:           "",
		Model:        r.Model,
		Created:      r.CreatedAt.Unix(),
		Message:      msg,
		FinishReason: finish,
		Usage: xl.Usage{
			PromptTokens:     r.PromptEvalCount,
			CompletionTokens: r.EvalCount,
			TotalTokens:      r.PromptEvalCount + r.EvalCount,
		},
	}, nil
}

// --- Emitter ---

func (Emitter) EmitChatRequest(req *xl.ChatRequest, kind xl.EndpointKind) ([]byte, error) {
	options := map[string]any{}
	if req.Temperature != nil {
		options["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		options["top_p"] = *req.TopP
	}
	if req.TopK != nil {
		options["top_k"] = *req.TopK
	}
	if req.MaxTokens != nil {
		options["num_predict"] = *req.MaxTokens
	}
	if req.Seed != nil {
		options["seed"] = *req.Seed
	}
	if len(req.Stop) > 0 {
		options["stop"] = req.Stop
	}

	if kind == xl.KindGenerate {
		var prompt strings.Builder
		for _, m := range req.Messages {
			for _, p := range m.Content {
				if p.Type == xl.PartText {
					prompt.WriteString(p.Text)
				}
			}
		}
		out := map[string]any{
			"model":  req.Model,
			"prompt": prompt.String(),
			"stream": req.Stream,
		}
		if req.System != "" {
			out["system"] = req.System
		}
		if len(options) > 0 {
			out["options"] = options
		}
		return json.Marshal(out)
	}

	var msgs []map[string]any
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		role := string(m.Role)
		if role == "tool" {
			// emit as role=tool with content = output
			var outStr string
			for _, p := range m.Content {
				if p.Type == xl.PartToolResult {
					outStr = p.Output
				}
			}
			msgs = append(msgs, map[string]any{"role": "tool", "content": outStr})
			continue
		}
		// user with tool_result parts (Anthropic style) → separate tool messages
		hasTR := false
		for _, p := range m.Content {
			if p.Type == xl.PartToolResult {
				hasTR = true
				break
			}
		}
		if hasTR && role == "user" {
			for _, p := range m.Content {
				switch p.Type {
				case xl.PartToolResult:
					msgs = append(msgs, map[string]any{"role": "tool", "content": p.Output})
				case xl.PartText:
					if p.Text != "" {
						msgs = append(msgs, map[string]any{"role": "user", "content": p.Text})
					}
				}
			}
			continue
		}
		om := map[string]any{"role": role}
		var text strings.Builder
		var calls []map[string]any
		for _, p := range m.Content {
			switch p.Type {
			case xl.PartText:
				text.WriteString(p.Text)
			case xl.PartToolUse:
				input := p.Input
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				calls = append(calls, map[string]any{
					"function": map[string]any{
						"name":      p.ToolName,
						"arguments": input,
					},
				})
			}
		}
		for _, tc := range m.ToolCalls {
			input := tc.Arguments
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			calls = append(calls, map[string]any{
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": input,
				},
			})
		}
		om["content"] = text.String()
		if len(calls) > 0 {
			om["tool_calls"] = calls
		}
		msgs = append(msgs, om)
	}

	out := map[string]any{
		"model":    req.Model,
		"messages": msgs,
		"stream":   req.Stream,
	}
	if len(options) > 0 {
		out["options"] = options
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
	return json.Marshal(out)
}

func (Emitter) EmitChatResponse(r *xl.ChatResponse) ([]byte, error) {
	msg := map[string]any{"role": "assistant", "content": ""}
	var text strings.Builder
	var calls []map[string]any
	for _, p := range r.Message.Content {
		switch p.Type {
		case xl.PartText:
			text.WriteString(p.Text)
		case xl.PartToolUse:
			input := p.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			calls = append(calls, map[string]any{
				"function": map[string]any{
					"name":      p.ToolName,
					"arguments": input,
				},
			})
		}
	}
	for _, tc := range r.Message.ToolCalls {
		input := tc.Arguments
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		calls = append(calls, map[string]any{
			"function": map[string]any{
				"name":      tc.Name,
				"arguments": input,
			},
		})
	}
	msg["content"] = text.String()
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}
	doneReason := "stop"
	switch r.FinishReason {
	case xl.FinishLength:
		doneReason = "length"
	case xl.FinishToolCalls:
		doneReason = "tool_calls"
	}
	created := time.Unix(r.Created, 0).UTC()
	if r.Created == 0 {
		created = time.Now().UTC()
	}
	out := map[string]any{
		"model":             r.Model,
		"created_at":        created.Format(time.RFC3339Nano),
		"message":           msg,
		"done":              true,
		"done_reason":       doneReason,
		"prompt_eval_count": r.Usage.PromptTokens,
		"eval_count":        r.Usage.CompletionTokens,
	}
	return json.Marshal(out)
}

var (
	_ xl.Parser  = Parser{}
	_ xl.Emitter = Emitter{}
)
