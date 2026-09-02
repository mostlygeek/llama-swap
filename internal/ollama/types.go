// Package ollama implements an Ollama-compatible API surface that translates
// to and from llama.cpp's OpenAI-compatible API. The package provides:
//
//   - Request translation (Ollama JSON -> OpenAI JSON) for inference endpoints
//   - Response translation (OpenAI JSON or SSE -> Ollama JSON or NDJSON)
//   - Native handlers for informational endpoints (tags, show, ps, version)
//   - 501 stubs for model management endpoints (pull, push, create, etc.)
//
// llama-swap routes requests by model name; this package only translates the
// wire protocol. The actual model swap, filter application, and forwarding
// continue to use the existing DispatchJSON path in proxy.ProxyManager.
package ollama

import "encoding/json"

// Message is a single chat message in an Ollama request or response.
// Images are base64-encoded payloads attached to user messages.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Images    []string   `json:"images,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type Tool struct {
	Type     string         `json:"type"`
	Function map[string]any `json:"function"`
}

// ChatRequest is the body of POST /api/chat.
type ChatRequest struct {
	Model     string          `json:"model"`
	Messages  []Message       `json:"messages"`
	Tools     []Tool          `json:"tools,omitempty"`
	Format    json.RawMessage `json:"format,omitempty"`
	Options   map[string]any  `json:"options,omitempty"`
	Stream    *bool           `json:"stream,omitempty"`
	KeepAlive json.RawMessage `json:"keep_alive,omitempty"`
	Think     json.RawMessage `json:"think,omitempty"`
}

// GenerateRequest is the body of POST /api/generate.
type GenerateRequest struct {
	Model     string          `json:"model"`
	Prompt    string          `json:"prompt"`
	Suffix    string          `json:"suffix,omitempty"`
	Images    []string        `json:"images,omitempty"`
	Format    json.RawMessage `json:"format,omitempty"`
	Options   map[string]any  `json:"options,omitempty"`
	System    string          `json:"system,omitempty"`
	Template  string          `json:"template,omitempty"`
	Stream    *bool           `json:"stream,omitempty"`
	Raw       bool            `json:"raw,omitempty"`
	KeepAlive json.RawMessage `json:"keep_alive,omitempty"`
}

// EmbedRequest is the body of POST /api/embed (the modern endpoint).
// Input may be a string or a []string; we keep it as RawMessage so the
// downstream OpenAI shape can mirror whatever the client sent.
type EmbedRequest struct {
	Model      string          `json:"model"`
	Input      json.RawMessage `json:"input"`
	Truncate   *bool           `json:"truncate,omitempty"`
	Options    map[string]any  `json:"options,omitempty"`
	KeepAlive  json.RawMessage `json:"keep_alive,omitempty"`
	Dimensions *int            `json:"dimensions,omitempty"`
}

// EmbeddingsRequest is the body of POST /api/embeddings (the legacy endpoint).
type EmbeddingsRequest struct {
	Model     string          `json:"model"`
	Prompt    string          `json:"prompt"`
	Options   map[string]any  `json:"options,omitempty"`
	KeepAlive json.RawMessage `json:"keep_alive,omitempty"`
}

// ChatResponse is the body of a non-streaming /api/chat reply.
// Streaming replies use the same shape, one JSON object per NDJSON line, with
// Done=false on intermediate frames and Done=true on the final frame.
type ChatResponse struct {
	Model              string  `json:"model"`
	CreatedAt          string  `json:"created_at"`
	Message            Message `json:"message"`
	Done               bool    `json:"done"`
	DoneReason         string  `json:"done_reason,omitempty"`
	TotalDuration      int64   `json:"total_duration,omitempty"`
	LoadDuration       int64   `json:"load_duration,omitempty"`
	PromptEvalCount    int     `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64   `json:"prompt_eval_duration,omitempty"`
	EvalCount          int     `json:"eval_count,omitempty"`
	EvalDuration       int64   `json:"eval_duration,omitempty"`
}

// GenerateResponse is the body of a non-streaming /api/generate reply.
type GenerateResponse struct {
	Model              string `json:"model"`
	CreatedAt          string `json:"created_at"`
	Response           string `json:"response"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason,omitempty"`
	Context            []int  `json:"context,omitempty"`
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalCount    int    `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64  `json:"prompt_eval_duration,omitempty"`
	EvalCount          int    `json:"eval_count,omitempty"`
	EvalDuration       int64  `json:"eval_duration,omitempty"`
}

// EmbedResponse is the body of POST /api/embed.
type EmbedResponse struct {
	Model           string      `json:"model"`
	Embeddings      [][]float64 `json:"embeddings"`
	TotalDuration   int64       `json:"total_duration,omitempty"`
	LoadDuration    int64       `json:"load_duration,omitempty"`
	PromptEvalCount int         `json:"prompt_eval_count,omitempty"`
}

// EmbeddingsResponse is the body of POST /api/embeddings (legacy, singular).
type EmbeddingsResponse struct {
	Embedding []float64 `json:"embedding"`
}

// TagsResponse is the body of GET /api/tags.
type TagsResponse struct {
	Models []TagModel `json:"models"`
}

type TagModel struct {
	Name       string     `json:"name"`
	Model      string     `json:"model"`
	ModifiedAt string     `json:"modified_at"`
	Size       int64      `json:"size"`
	Digest     string     `json:"digest"`
	Details    TagDetails `json:"details"`
}

type TagDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

// ShowResponse is the body of POST /api/show.
type ShowResponse struct {
	License      string         `json:"license,omitempty"`
	Modelfile    string         `json:"modelfile"`
	Parameters   string         `json:"parameters"`
	Template     string         `json:"template"`
	Details      TagDetails     `json:"details"`
	ModelInfo    map[string]any `json:"model_info,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
}

// PsResponse is the body of GET /api/ps.
type PsResponse struct {
	Models []PsModel `json:"models"`
}

type PsModel struct {
	Name      string     `json:"name"`
	Model     string     `json:"model"`
	Size      int64      `json:"size"`
	Digest    string     `json:"digest"`
	Details   TagDetails `json:"details"`
	ExpiresAt string     `json:"expires_at"`
	SizeVRAM  int64      `json:"size_vram"`
}

// VersionResponse is the body of GET /api/version.
type VersionResponse struct {
	Version string `json:"version"`
}

// ErrorResponse is what the Ollama server returns on failure.
type ErrorResponse struct {
	Error string `json:"error"`
}
