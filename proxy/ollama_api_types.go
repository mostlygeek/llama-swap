package proxy

import (
	"encoding/json"
	"time"
)

// OllamaErrorResponse is the standard error format for Ollama API.
type OllamaErrorResponse struct {
	Error string `json:"error"`
}

// OllamaVersionResponse defines the structure for the /api/version endpoint.
type OllamaVersionResponse struct {
	Version string `json:"version"`
}

// OllamaGenerateRequest describes a request to /api/generate.
type OllamaGenerateRequest struct {
	Model     string                 `json:"model"`
	Prompt    string                 `json:"prompt"`
	System    string                 `json:"system,omitempty"`
	Template  string                 `json:"template,omitempty"`
	Context   []int                  `json:"context,omitempty"`
	Stream    *bool                  `json:"stream,omitempty"`
	Raw       bool                   `json:"raw,omitempty"`
	Format    string                 `json:"format,omitempty"` // e.g., "json"
	Images    []string               `json:"images,omitempty"` // Base64 encoded images
	KeepAlive string                 `json:"keep_alive,omitempty"`
	Options   map[string]interface{} `json:"options,omitempty"`
}

// OllamaGenerateResponse is the response from /api/generate.
type OllamaGenerateResponse struct {
	Model              string    `json:"model"`
	CreatedAt          time.Time `json:"created_at"`
	Response           string    `json:"response,omitempty"` // Empty if streaming and no content yet
	Done               bool      `json:"done"`
	DoneReason         string    `json:"done_reason,omitempty"`
	Context            []int     `json:"context,omitempty"`
	TotalDuration      int64     `json:"total_duration,omitempty"` // Nanoseconds
	LoadDuration       int64     `json:"load_duration,omitempty"`  // Nanoseconds
	PromptEvalCount    int       `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64     `json:"prompt_eval_duration,omitempty"` // Nanoseconds
	EvalCount          int       `json:"eval_count,omitempty"`
	EvalDuration       int64     `json:"eval_duration,omitempty"` // Nanoseconds
}

// OllamaMessage represents a single message in a chat.
type OllamaMessage struct {
	Role    string   `json:"role"` // "system", "user", or "assistant"
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // Base64 encoded images
}

// OllamaChatRequest describes a request to /api/chat.
type OllamaChatRequest struct {
	Model     string                 `json:"model"`
	Messages  []OllamaMessage        `json:"messages"`
	Stream    *bool                  `json:"stream,omitempty"`
	Format    string                 `json:"format,omitempty"` // e.g., "json"
	KeepAlive string                 `json:"keep_alive,omitempty"`
	Options   map[string]interface{} `json:"options,omitempty"`
}

// OllamaChatResponse is the response from /api/chat.
type OllamaChatResponse struct {
	Model              string        `json:"model"`
	CreatedAt          time.Time     `json:"created_at"`
	Message            OllamaMessage `json:"message,omitempty"` // Empty if streaming and no content yet
	Done               bool          `json:"done"`
	DoneReason         string        `json:"done_reason,omitempty"`
	TotalDuration      int64         `json:"total_duration,omitempty"` // Nanoseconds
	LoadDuration       int64         `json:"load_duration,omitempty"`  // Nanoseconds
	PromptEvalCount    int           `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64         `json:"prompt_eval_duration,omitempty"` // Nanoseconds
	EvalCount          int           `json:"eval_count,omitempty"`
	EvalDuration       int64         `json:"eval_duration,omitempty"` // Nanoseconds
}

// OllamaListTagsResponse is the response from /api/tags.
type OllamaListTagsResponse struct {
	Models []OllamaModelResponse `json:"models"`
}

// OllamaModelResponse describes a single model in the list.
type OllamaModelResponse struct {
	Name       string             `json:"name"`  // Full model name with tag
	Model      string             `json:"model"` // Model name (e.g. "llama2:7b")
	ModifiedAt time.Time          `json:"modified_at"`
	Size       int64              `json:"size"`   // Placeholder, llama-swap doesn't track exact size
	Digest     string             `json:"digest"` // Placeholder or hash of model ID
	Details    OllamaModelDetails `json:"details"`
}

// OllamaModelDetails provides more details about a model.
type OllamaModelDetails struct {
	ParentModel       string   `json:"parent_model,omitempty"`
	Format            string   `json:"format,omitempty"`             // e.g., "gguf"
	Family            string   `json:"family,omitempty"`             // e.g., "llama"
	Families          []string `json:"families,omitempty"`           // e.g., ["llama"]
	ParameterSize     string   `json:"parameter_size,omitempty"`     // e.g., "7B"
	QuantizationLevel string   `json:"quantization_level,omitempty"` // e.g., "Q4_0"
}

// OllamaShowRequest is the request for /api/show.
type OllamaShowRequest struct {
	Model string `json:"model,omitempty"` // Ollama uses 'model' in newer versions
	Name  string `json:"name,omitempty"`  // Ollama used 'name' in older versions
}

// OllamaShowResponse is the response from /api/show.
type OllamaShowResponse struct {
	License       string                 `json:"license,omitempty"`
	Modelfile     string                 `json:"modelfile,omitempty"`
	Parameters    string                 `json:"parameters,omitempty"`
	Template      string                 `json:"template,omitempty"`
	System        string                 `json:"system,omitempty"`
	Details       OllamaModelDetails     `json:"details,omitempty"`
	Messages      []OllamaMessage        `json:"messages,omitempty"` // For chat models
	ModelInfo     map[string]interface{} `json:"model_info,omitempty"`
	ProjectorInfo map[string]interface{} `json:"projector_info,omitempty"` // For multimodal models
	ModifiedAt    time.Time              `json:"modified_at,omitempty"`
}

// OllamaProcessResponse is the response from /api/ps.
type OllamaProcessResponse struct {
	Models []OllamaProcessModelResponse `json:"models"`
}

// OllamaProcessModelResponse describes a running model process.
type OllamaProcessModelResponse struct {
	Name      string             `json:"name"`
	Model     string             `json:"model"`
	Size      int64              `json:"size"`   // Placeholder
	Digest    string             `json:"digest"` // Placeholder
	Details   OllamaModelDetails `json:"details"`
	ExpiresAt time.Time          `json:"expires_at"` // Placeholder or calculated if TTL
	SizeVRAM  int64              `json:"size_vram"`  // Placeholder
}

// --- Helper types for transforming OpenAI stream to Ollama stream ---

// OpenAIChatCompletionStreamChoiceDelta is part of an OpenAI stream event.
type OpenAIChatCompletionStreamChoiceDelta struct {
	Content string `json:"content,omitempty"`
	Role    string `json:"role,omitempty"`
}

// OpenAIChatCompletionStreamChoice is part of an OpenAI stream event.
type OpenAIChatCompletionStreamChoice struct {
	Index        int                                   `json:"index"`
	Delta        OpenAIChatCompletionStreamChoiceDelta `json:"delta"`
	FinishReason string                                `json:"finish_reason,omitempty"`
}

// OpenAIStreamingChatResponse is a typical OpenAI chat completion stream event.
type OpenAIStreamingChatResponse struct {
	ID      string                             `json:"id"`
	Object  string                             `json:"object"`
	Created int64                              `json:"created"`
	Model   string                             `json:"model"`
	Choices []OpenAIChatCompletionStreamChoice `json:"choices"`
	// Usage can appear in the last message if supported by the backend
	Usage *OpenAIUsage `json:"usage,omitempty"`
}

// OpenAICompletionStreamChoice is part of an OpenAI legacy completion stream event.
type OpenAICompletionStreamChoice struct {
	Text         string `json:"text"`
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason,omitempty"`
}

// OpenAIStreamingCompletionResponse is a typical OpenAI legacy completion stream event.
type OpenAIStreamingCompletionResponse struct {
	ID      string                         `json:"id"`
	Object  string                         `json:"object"`
	Created int64                          `json:"created"`
	Model   string                         `json:"model"`
	Choices []OpenAICompletionStreamChoice `json:"choices"`
	// Usage can appear in the last message if supported by the backend
	Usage *OpenAIUsage `json:"usage,omitempty"`
}

// OpenAIUsage represents token usage statistics.
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIChatCompletionResponse is a non-streaming OpenAI chat response.
type OpenAIChatCompletionResponse struct {
	ID      string                               `json:"id"`
	Object  string                               `json:"object"`
	Created int64                                `json:"created"`
	Model   string                               `json:"model"`
	Choices []OpenAIChatCompletionResponseChoice `json:"choices"`
	Usage   OpenAIUsage                          `json:"usage"`
}

// OpenAIChatCompletionMessage is the message structure in a non-streaming OpenAI response.
type OpenAIChatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIChatCompletionResponseChoice is part of a non-streaming OpenAI chat response.
type OpenAIChatCompletionResponseChoice struct {
	Index        int                         `json:"index"`
	Message      OpenAIChatCompletionMessage `json:"message"`
	FinishReason string                      `json:"finish_reason"`
}

// OpenAICompletionResponse is a non-streaming OpenAI legacy completion response.
type OpenAICompletionResponse struct {
	ID      string                         `json:"id"`
	Object  string                         `json:"object"`
	Created int64                          `json:"created"`
	Model   string                         `json:"model"`
	Choices []OpenAICompletionStreamChoice `json:"choices"` // Re-use stream choice for simplicity
	Usage   OpenAIUsage                    `json:"usage"`
}

// Helper to convert OpenAI stream finish_reason to Ollama done_reason
func openAIFinishReasonToOllama(reason string) string {
	switch reason {
	case "stop":
		return "stop"
	case "length":
		return "length"
	case "content_filter":
		return "content_filter"
	case "tool_calls":
		return "tool_calls" // Or handle more specifically if llama-swap supports tools
	default:
		if reason != "" {
			return "unknown" // Or pass through if it's a custom reason
		}
		return ""
	}
}

// Helper to convert OpenAI role to Ollama role (should be compatible but good for consistency)
func openAIRoleToOllama(role string) string {
	switch role {
	case "system":
		return "system"
	case "user":
		return "user"
	case "assistant":
		return "assistant"
	default:
		return role // Pass through if unknown, though unlikely for standard OpenAI
	}
}

// Helper to convert Ollama messages to OpenAI messages
func ollamaMessagesToOpenAI(ollamaMsgs []OllamaMessage) []map[string]interface{} {
	openAIMsgs := make([]map[string]interface{}, len(ollamaMsgs))
	for i, msg := range ollamaMsgs {
		// Basic transformation. Image handling would require more complex logic
		// if the target OpenAI model supports multimodal input in this specific way.
		// For now, we assume text-only or that the backend handles image URIs if passed.
		openAIMsgs[i] = map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
		// Note: OpenAI's vision models expect images in a specific content array format.
		// This basic conversion won't handle that directly.
		// If msg.Images is populated, a more sophisticated conversion is needed.
	}
	return openAIMsgs
}

// Helper to create a JSON request body
func createOpenAIRequestBody(modelName string, messages []map[string]interface{}, stream bool, options map[string]interface{}) ([]byte, error) {
	requestBody := map[string]interface{}{
		"model":    modelName,
		"messages": messages,
		"stream":   stream,
	}

	// Merge options from Ollama request if any
	// This is a simple merge; more sophisticated mapping might be needed for specific options
	if options != nil {
		for k, v := range options {
			// Avoid overwriting core fields unless explicitly intended
			if _, exists := requestBody[k]; !exists {
				requestBody[k] = v
			}
		}
	}

	return json.Marshal(requestBody)
}

func createOpenAILegacyCompletionRequestBody(modelName string, prompt string, stream bool, options map[string]interface{}) ([]byte, error) {
	requestBody := map[string]interface{}{
		"model":  modelName,
		"prompt": prompt,
		"stream": stream,
	}
	if options != nil {
		for k, v := range options {
			if _, exists := requestBody[k]; !exists {
				requestBody[k] = v
			}
		}
	}
	return json.Marshal(requestBody)
}
