package ollama

import (
	"encoding/json"
	"fmt"
	"time"
)

// optionsMap defines the Ollama options.* -> OpenAI top-level mapping that this
// package forwards. Unknown options keys are dropped silently — start strict,
// expand based on real client behavior.
var optionsMap = map[string]string{
	"temperature":       "temperature",
	"top_p":             "top_p",
	"top_k":             "top_k",
	"min_p":             "min_p",
	"num_predict":       "max_tokens",
	"stop":              "stop",
	"seed":              "seed",
	"presence_penalty":  "presence_penalty",
	"frequency_penalty": "frequency_penalty",
	"repeat_penalty":    "repeat_penalty",
}

// applyOptions copies known options.* keys from the Ollama options map to the
// flat OpenAI request map. Unknown keys are silently dropped.
func applyOptions(opts map[string]any, out map[string]any) {
	for ollamaKey, openaiKey := range optionsMap {
		if v, ok := opts[ollamaKey]; ok {
			out[openaiKey] = v
		}
	}
}

// applyFormat translates the Ollama `format` field into an OpenAI
// `response_format` field. Ollama accepts either the string "json" or a JSON
// schema object; both shapes are supported.
func applyFormat(format json.RawMessage, out map[string]any) error {
	if len(format) == 0 {
		return nil
	}
	// Try string first.
	var asString string
	if err := json.Unmarshal(format, &asString); err == nil {
		if asString == "json" {
			out["response_format"] = map[string]any{"type": "json_object"}
		}
		return nil
	}
	// Otherwise treat as schema object.
	var asObject map[string]any
	if err := json.Unmarshal(format, &asObject); err != nil {
		return fmt.Errorf("ollama: format must be \"json\" or a JSON schema object: %w", err)
	}
	out["response_format"] = map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "response",
			"schema": asObject,
			"strict": true,
		},
	}
	return nil
}

// convertMessages turns Ollama messages into OpenAI messages. Images attached
// to a user message are emitted as content parts of type image_url, matching
// OpenAI's vision API. Tool calls are passed through with minor renaming.
func convertMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		om := map[string]any{"role": m.Role}
		if len(m.Images) > 0 && m.Role == "user" {
			parts := []map[string]any{{"type": "text", "text": m.Content}}
			for _, img := range m.Images {
				parts = append(parts, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": "data:image/jpeg;base64," + img,
					},
				})
			}
			om["content"] = parts
		} else {
			om["content"] = m.Content
		}
		if m.ToolName != "" {
			om["name"] = m.ToolName
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				argsBytes, _ := json.Marshal(tc.Function.Arguments)
				calls = append(calls, map[string]any{
					"id":   fmt.Sprintf("call_%d", i),
					"type": "function",
					"function": map[string]any{
						"name":      tc.Function.Name,
						"arguments": string(argsBytes),
					},
				})
			}
			om["tool_calls"] = calls
		}
		out = append(out, om)
	}
	return out
}

// TranslateChatRequest converts an /api/chat body to a /v1/chat/completions body.
func TranslateChatRequest(req *ChatRequest) ([]byte, error) {
	out := map[string]any{
		"model":    req.Model,
		"messages": convertMessages(req.Messages),
	}
	if req.Stream != nil {
		out["stream"] = *req.Stream
		// Ask llama.cpp to emit a final SSE chunk with token usage so the
		// translator can populate Ollama's *_count fields.
		if *req.Stream {
			out["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	if len(req.Tools) > 0 {
		out["tools"] = req.Tools
	}
	applyOptions(req.Options, out)
	if err := applyFormat(req.Format, out); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// TranslateGenerateRequest converts an /api/generate body to a
// /v1/chat/completions body by constructing a single-message conversation.
// `raw:true` skips the system message and any templating.
func TranslateGenerateRequest(req *GenerateRequest) ([]byte, error) {
	var messages []map[string]any
	if req.Raw {
		messages = []map[string]any{
			{"role": "user", "content": req.Prompt},
		}
	} else {
		if req.System != "" {
			messages = append(messages, map[string]any{
				"role":    "system",
				"content": req.System,
			})
		}
		userMsg := map[string]any{"role": "user"}
		if len(req.Images) > 0 {
			parts := []map[string]any{{"type": "text", "text": req.Prompt}}
			for _, img := range req.Images {
				parts = append(parts, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": "data:image/jpeg;base64," + img,
					},
				})
			}
			userMsg["content"] = parts
		} else {
			userMsg["content"] = req.Prompt
		}
		messages = append(messages, userMsg)
	}

	out := map[string]any{
		"model":    req.Model,
		"messages": messages,
	}
	if req.Stream != nil {
		out["stream"] = *req.Stream
		if *req.Stream {
			out["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	if req.Suffix != "" {
		out["suffix"] = req.Suffix
	}
	applyOptions(req.Options, out)
	if err := applyFormat(req.Format, out); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// TranslateEmbedRequest converts /api/embed to /v1/embeddings. The input
// field passes through unchanged (string or array).
func TranslateEmbedRequest(req *EmbedRequest) ([]byte, error) {
	out := map[string]any{"model": req.Model}
	if len(req.Input) > 0 {
		var raw any
		if err := json.Unmarshal(req.Input, &raw); err != nil {
			return nil, fmt.Errorf("ollama: invalid input field: %w", err)
		}
		out["input"] = raw
	}
	if req.Dimensions != nil {
		out["dimensions"] = *req.Dimensions
	}
	return json.Marshal(out)
}

// TranslateEmbeddingsRequest converts the legacy /api/embeddings (singular)
// to /v1/embeddings. `prompt` becomes `input` (a string).
func TranslateEmbeddingsRequest(req *EmbeddingsRequest) ([]byte, error) {
	out := map[string]any{
		"model": req.Model,
		"input": req.Prompt,
	}
	return json.Marshal(out)
}

// openaiChatResponse is the subset of fields the translator reads from a
// non-streaming /v1/chat/completions reply.
type openaiChatResponse struct {
	Choices []struct {
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// TranslateChatResponse converts a non-streaming /v1/chat/completions reply
// into a non-streaming /api/chat reply.
func TranslateChatResponse(openaiBody []byte, model string, totalDuration time.Duration) ([]byte, error) {
	var resp openaiChatResponse
	if err := json.Unmarshal(openaiBody, &resp); err != nil {
		return nil, fmt.Errorf("ollama: invalid upstream JSON: %w", err)
	}
	out := ChatResponse{
		Model:           model,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Done:            true,
		DoneReason:      "stop",
		TotalDuration:   totalDuration.Nanoseconds(),
		PromptEvalCount: resp.Usage.PromptTokens,
		EvalCount:       resp.Usage.CompletionTokens,
	}
	if len(resp.Choices) > 0 {
		out.Message = Message{
			Role:    resp.Choices[0].Message.Role,
			Content: resp.Choices[0].Message.Content,
		}
		if out.Message.Role == "" {
			out.Message.Role = "assistant"
		}
		if fr := resp.Choices[0].FinishReason; fr != "" {
			out.DoneReason = fr
		}
		for _, tc := range resp.Choices[0].Message.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			out.Message.ToolCalls = append(out.Message.ToolCalls, ToolCall{
				Function: ToolCallFunction{Name: tc.Function.Name, Arguments: args},
			})
		}
	} else {
		out.Message = Message{Role: "assistant"}
	}
	return json.Marshal(out)
}

// TranslateGenerateResponse converts a non-streaming /v1/chat/completions reply
// into a non-streaming /api/generate reply.
func TranslateGenerateResponse(openaiBody []byte, model string, totalDuration time.Duration) ([]byte, error) {
	var resp openaiChatResponse
	if err := json.Unmarshal(openaiBody, &resp); err != nil {
		return nil, fmt.Errorf("ollama: invalid upstream JSON: %w", err)
	}
	out := GenerateResponse{
		Model:           model,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Done:            true,
		DoneReason:      "stop",
		TotalDuration:   totalDuration.Nanoseconds(),
		PromptEvalCount: resp.Usage.PromptTokens,
		EvalCount:       resp.Usage.CompletionTokens,
	}
	if len(resp.Choices) > 0 {
		out.Response = resp.Choices[0].Message.Content
		if fr := resp.Choices[0].FinishReason; fr != "" {
			out.DoneReason = fr
		}
	}
	return json.Marshal(out)
}

// openaiEmbedResponse is the subset of fields the translator reads from a
// /v1/embeddings reply.
type openaiEmbedResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}

// TranslateEmbedResponse converts a /v1/embeddings reply into an /api/embed
// reply (the embeddings field is a [][]float64).
func TranslateEmbedResponse(openaiBody []byte, model string, totalDuration time.Duration) ([]byte, error) {
	var resp openaiEmbedResponse
	if err := json.Unmarshal(openaiBody, &resp); err != nil {
		return nil, fmt.Errorf("ollama: invalid upstream JSON: %w", err)
	}
	out := EmbedResponse{
		Model:           model,
		Embeddings:      make([][]float64, 0, len(resp.Data)),
		TotalDuration:   totalDuration.Nanoseconds(),
		PromptEvalCount: resp.Usage.PromptTokens,
	}
	for _, d := range resp.Data {
		out.Embeddings = append(out.Embeddings, d.Embedding)
	}
	return json.Marshal(out)
}

// TranslateEmbeddingsResponse converts a /v1/embeddings reply into an
// /api/embeddings reply (legacy: single embedding field).
func TranslateEmbeddingsResponse(openaiBody []byte) ([]byte, error) {
	var resp openaiEmbedResponse
	if err := json.Unmarshal(openaiBody, &resp); err != nil {
		return nil, fmt.Errorf("ollama: invalid upstream JSON: %w", err)
	}
	out := EmbeddingsResponse{}
	if len(resp.Data) > 0 {
		out.Embedding = resp.Data[0].Embedding
	} else {
		out.Embedding = []float64{}
	}
	return json.Marshal(out)
}
