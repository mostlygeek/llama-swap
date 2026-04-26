// Package translate contains the canonical, OpenAI-shaped intermediate
// representation used to translate chat requests and responses between
// the OpenAI, Anthropic and Ollama wire protocols.
//
// Parsers turn a protocol-specific request/response (or single stream frame)
// into IR. Emitters turn IR back into a target protocol's wire format.
// Translators therefore run as O(N) adapters against the IR rather than
// O(N^2) pairwise transformers.
package translate

import "encoding/json"

// Role identifies who produced a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// PartType discriminates entries in Message.Content.
type PartType string

const (
	PartText       PartType = "text"
	PartToolUse    PartType = "tool_use"
	PartToolResult PartType = "tool_result"
	PartThinking   PartType = "thinking"
)

// Part is a single content block in a Message. Text blocks use Text;
// tool_use blocks use ToolUseID + ToolName + Input; tool_result blocks
// use ToolUseID + Output (+ IsError). Thinking blocks carry Text.
type Part struct {
	Type      PartType        `json:"type"`
	Text      string          `json:"text,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    string          `json:"output,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// ToolCall is a single assistant-issued tool invocation surfaced at the
// Message level for OpenAI-style consumers; Anthropic and Ollama adapters
// fold these into Part slices as needed.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Message is a single entry in ChatRequest.Messages or the completion
// carried by ChatResponse.
type Message struct {
	Role       Role       `json:"role"`
	Content    []Part     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// Tool is a function/tool the model may invoke.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
}

// ChatRequest is the canonical chat request shape.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	System      string    `json:"system,omitempty"`
	Tools       []Tool    `json:"tools,omitempty"`
	ToolChoice  any       `json:"tool_choice,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	TopK        *int      `json:"top_k,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	Stop        []string  `json:"stop,omitempty"`
	Seed        *int64    `json:"seed,omitempty"`

	// Extra holds wire-format-specific fields that the parser did not
	// promote into typed IR. Emitters may copy these through when the
	// target protocol understands them.
	Extra map[string]any `json:"extra,omitempty"`
}

// Usage reports token accounting.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// FinishReason is normalised across protocols.
type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishLength    FinishReason = "length"
	FinishToolCalls FinishReason = "tool_calls"
	FinishError     FinishReason = "error"
)

// ChatResponse is the canonical non-streaming chat response.
type ChatResponse struct {
	ID           string       `json:"id"`
	Model        string       `json:"model"`
	Created      int64        `json:"created"`
	FinishReason FinishReason `json:"finish_reason"`
	Message      Message      `json:"message"`
	Usage        Usage        `json:"usage"`
}

// StreamEventType discriminates frames in a translated SSE/NDJSON stream.
type StreamEventType string

const (
	StreamStart         StreamEventType = "start"
	StreamTextDelta     StreamEventType = "text_delta"
	StreamToolUseStart  StreamEventType = "tool_use_start"
	StreamToolArgsDelta StreamEventType = "tool_args_delta"
	StreamToolUseStop   StreamEventType = "tool_use_stop"
	StreamUsage         StreamEventType = "usage"
	StreamStop          StreamEventType = "stop"
	StreamError         StreamEventType = "error"
)

// StreamEvent is one frame in a translated chat stream. It carries only
// the fields relevant to its Type.
type StreamEvent struct {
	Type         StreamEventType `json:"type"`
	Index        int             `json:"index,omitempty"`
	Text         string          `json:"text,omitempty"`
	ToolCall     *ToolCall       `json:"tool_call,omitempty"`
	ArgsDelta    string          `json:"args_delta,omitempty"`
	Usage        *Usage          `json:"usage,omitempty"`
	FinishReason FinishReason    `json:"finish_reason,omitempty"`
	Err          string          `json:"error,omitempty"`

	// Model is set on StreamStart when known, to allow emitters that
	// need a model name at frame-emission time.
	Model string `json:"model,omitempty"`
	// ID is set on StreamStart when known (e.g. chatcmpl-...).
	ID string `json:"id,omitempty"`
}
