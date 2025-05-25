package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func (pm *ProxyManager) sendOllamaError(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, OllamaErrorResponse{Error: message})
}

func (pm *ProxyManager) ollamaNotImplementedHandler(c *gin.Context) {
	pm.sendOllamaError(c, http.StatusNotImplemented, "This Ollama API endpoint is not implemented in llama-swap.")
}

func (pm *ProxyManager) ollamaVersionHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, OllamaVersionResponse{Version: "0.0.0"})
	}
}

func (pm *ProxyManager) ollamaHeartbeatHandler(c *gin.Context) {
	c.String(http.StatusOK, "Ollama is running") // Ollama server returns this string
}

func (pm *ProxyManager) ollamaListTagsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		models := []OllamaModelResponse{}
		now := time.Now().UTC() // Use a consistent timestamp

		pm.RLock() // Lock for reading pm.config.Models
		for id, modelCfg := range pm.config.Models {
			if modelCfg.Unlisted {
				continue
			}

			// Basic details, can be enhanced if more info is added to ModelConfig
			details := OllamaModelDetails{
				Format:            "gguf", // Common default, or make configurable
				Family:            "unknown",
				ParameterSize:     "unknown",
				QuantizationLevel: "unknown",
			}
			// Try to infer from ID if typical patterns are used, e.g., "llama2-7b-q4_0"
			parts := strings.Split(id, "-")
			if len(parts) > 0 {
				details.Family = parts[0]
			}
			if len(parts) > 1 {
				details.ParameterSize = parts[1] // This is a rough guess
			}

			models = append(models, OllamaModelResponse{
				Name:       id,                    // Ollama uses full name like "llama2:latest"
				Model:      id,                    // Model name without tag, for llama-swap it's the same as ID
				ModifiedAt: now,                   // Placeholder, could use config file mod time
				Size:       0,                     // Placeholder, llama-swap doesn't track this
				Digest:     fmt.Sprintf("%x", id), // Placeholder digest
				Details:    details,
			})
		}
		pm.RUnlock()

		c.JSON(http.StatusOK, OllamaListTagsResponse{Models: models})
	}
}

func (pm *ProxyManager) ollamaShowHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req OllamaShowRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			pm.sendOllamaError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err))
			return
		}

		modelName := req.Model
		if modelName == "" {
			modelName = req.Name // For compatibility with older Ollama clients
		}

		if modelName == "" {
			pm.sendOllamaError(c, http.StatusBadRequest, "Model name is required.")
			return
		}

		pm.RLock()
		modelCfg, id, found := pm.config.FindConfig(modelName)
		pm.RUnlock()

		if !found {
			pm.sendOllamaError(c, http.StatusNotFound, fmt.Sprintf("Model '%s' not found.", modelName))
			return
		}

		details := OllamaModelDetails{
			Format:            "gguf",
			Family:            "unknown",
			ParameterSize:     "unknown",
			QuantizationLevel: "unknown",
		}
		parts := strings.Split(id, "-")
		if len(parts) > 0 {
			details.Family = parts[0]
		}
		if len(parts) > 1 {
			details.ParameterSize = parts[1]
		}

		// Construct a basic modelfile representation
		var modelfileBuilder strings.Builder
		modelfileBuilder.WriteString(fmt.Sprintf("FROM %s\n\n", id))
		if modelCfg.Cmd != "" {
			modelfileBuilder.WriteString(fmt.Sprintf("# CMD %s\n", modelCfg.Cmd))
		}
		if modelCfg.Proxy != "" {
			modelfileBuilder.WriteString(fmt.Sprintf("# PROXY %s\n", modelCfg.Proxy))
		}
		if len(modelCfg.Env) > 0 {
			modelfileBuilder.WriteString("\n# ENVIRONMENT VARIABLES\n")
			for _, envVar := range modelCfg.Env {
				modelfileBuilder.WriteString(fmt.Sprintf("# ENV %s\n", envVar))
			}
		}

		resp := OllamaShowResponse{
			Modelfile:  modelfileBuilder.String(),
			Parameters: strings.Join(modelCfg.Env, "\n"), // Simple representation
			Template:   "",                               // llama-swap doesn't manage templates this way
			System:     "",                               // llama-swap doesn't manage system prompts this way
			Details:    details,
			ModifiedAt: time.Now().UTC(), // Placeholder
			ModelInfo: map[string]interface{}{
				"cmd":   modelCfg.Cmd,
				"proxy": modelCfg.Proxy,
			},
		}

		c.JSON(http.StatusOK, resp)
	}
}

func (pm *ProxyManager) ollamaPSHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		runningModels := []OllamaProcessModelResponse{}
		now := time.Now().UTC()

		pm.RLock()
		for _, group := range pm.processGroups {
			group.Lock() // Lock group while iterating its processes
			for modelID, process := range group.processes {
				if process.CurrentState() == StateReady {
					details := OllamaModelDetails{
						Format:            "gguf",
						Family:            "unknown",
						ParameterSize:     "unknown",
						QuantizationLevel: "unknown",
					}
					parts := strings.Split(modelID, "-")
					if len(parts) > 0 {
						details.Family = parts[0]
					}
					if len(parts) > 1 {
						details.ParameterSize = parts[1]
					}

					expiresAt := time.Time{} // Zero time if no TTL
					if process.config.UnloadAfter > 0 {
						// This is a rough estimation, Ollama's expiry is more dynamic
						expiresAt = process.lastRequestHandled.Add(time.Duration(process.config.UnloadAfter) * time.Second)
						if expiresAt.Before(now) && !process.lastRequestHandled.IsZero() { // If already past, but was used
							expiresAt = now.Add(time.Duration(process.config.UnloadAfter) * time.Second)
						} else if process.lastRequestHandled.IsZero() { // Never used, but ready
							expiresAt = now.Add(time.Duration(process.config.UnloadAfter) * time.Second)
						}
					}

					runningModels = append(runningModels, OllamaProcessModelResponse{
						Name:      modelID,
						Model:     modelID,
						Size:      0,                          // Placeholder
						Digest:    fmt.Sprintf("%x", modelID), // Placeholder
						Details:   details,
						ExpiresAt: expiresAt,
						SizeVRAM:  0, // Placeholder
					})
				}
			}
			group.Unlock()
		}
		pm.RUnlock()

		c.JSON(http.StatusOK, OllamaProcessResponse{Models: runningModels})
	}
}

// transformingResponseWriter captures and transforms SSE stream from OpenAI to Ollama format
type transformingResponseWriter struct {
	ginWriter gin.ResponseWriter
	modelName string
	buffer    bytes.Buffer // To handle partial SSE events
	isChat    bool         // True for chat, false for generate
}

func newTransformingResponseWriter(writer gin.ResponseWriter, modelName string, isChat bool) *transformingResponseWriter {
	return &transformingResponseWriter{
		ginWriter: writer,
		modelName: modelName,
		isChat:    isChat,
	}
}

func (trw *transformingResponseWriter) Header() http.Header {
	return trw.ginWriter.Header()
}

func (trw *transformingResponseWriter) Write(data []byte) (int, error) {
	// Append data to internal buffer
	return trw.buffer.Write(data)
}

func (trw *transformingResponseWriter) WriteHeader(statusCode int) {
	trw.ginWriter.WriteHeader(statusCode)
}

func (trw *transformingResponseWriter) Flush() {
	scanner := bufio.NewScanner(&trw.buffer)
	var processedBuffer bytes.Buffer // Store fully processed lines to write

	var unprocessedSuffix []byte // Store any partial line at the end

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")
			if jsonData == "[DONE]" {
				// This is the end of the OpenAI stream.
				// For Ollama, the 'done: true' is part of the last content message.
				// We might have already sent it if finish_reason was present.
				// If not, we might need to send a final empty message with done:true,
				// but typically the last content chunk from OpenAI has finish_reason.
				break // Stop processing, [DONE] is handled by finish_reason
			}

			var ollamaChunkJSON []byte
			var err error

			if trw.isChat {
				var openAIChatChunk OpenAIStreamingChatResponse
				if err = json.Unmarshal([]byte(jsonData), &openAIChatChunk); err == nil {
					if len(openAIChatChunk.Choices) > 0 {
						choice := openAIChatChunk.Choices[0]
						ollamaResp := OllamaChatResponse{
							Model:     trw.modelName, // Or openAIChatChunk.Model if preferred
							CreatedAt: time.Now().UTC(),
							Message: OllamaMessage{
								Role:    openAIRoleToOllama(choice.Delta.Role),
								Content: choice.Delta.Content,
							},
							Done:       choice.FinishReason != "",
							DoneReason: openAIFinishReasonToOllama(choice.FinishReason),
						}
						if choice.Delta.Role == "" && ollamaResp.Message.Role == "" {
							ollamaResp.Message.Role = "assistant" // Default role for content delta
						}
						if openAIChatChunk.Usage != nil {
							ollamaResp.PromptEvalCount = openAIChatChunk.Usage.PromptTokens
							ollamaResp.EvalCount = openAIChatChunk.Usage.CompletionTokens
						}

						ollamaChunkJSON, err = json.Marshal(ollamaResp)
					}
				}
			} else { // /api/generate
				var openAIGenChunk OpenAIStreamingCompletionResponse
				if err = json.Unmarshal([]byte(jsonData), &openAIGenChunk); err == nil {
					if len(openAIGenChunk.Choices) > 0 {
						choice := openAIGenChunk.Choices[0]
						ollamaResp := OllamaGenerateResponse{
							Model:      trw.modelName, // Or openAIGenChunk.Model
							CreatedAt:  time.Now().UTC(),
							Response:   choice.Text,
							Done:       choice.FinishReason != "",
							DoneReason: openAIFinishReasonToOllama(choice.FinishReason),
						}
						if openAIGenChunk.Usage != nil {
							ollamaResp.PromptEvalCount = openAIGenChunk.Usage.PromptTokens
							ollamaResp.EvalCount = openAIGenChunk.Usage.CompletionTokens
						}
						ollamaChunkJSON, err = json.Marshal(ollamaResp)
					}
				}
			}

			if err == nil && ollamaChunkJSON != nil {
				processedBuffer.Write(ollamaChunkJSON)
				processedBuffer.WriteString("\n")
			} else if err != nil {
				// Log error, decide if to stop or continue
				fmt.Fprintf(trw.ginWriter, "{\"error\":\"Error transforming stream: %v\"}\n", err)
			}
		} else if line != "" {
			// This might be an error from the upstream if not SSE, or some other non-SSE data.
			// Or it could be a partial line if the original Write didn't end with \n.
			// For now, let's assume full lines from scanner.Scan().
			// If it's an error from upstream, it should ideally be JSON.
			var errResp OllamaErrorResponse
			if json.Unmarshal([]byte(line), &errResp) == nil && errResp.Error != "" {
				processedBuffer.Write([]byte(line)) // Pass through JSON error
				processedBuffer.WriteString("\n")
			} else {
				// Non-SSE, non-JSON error line, or unexpected format.
				// Could write as a generic error or ignore.
				// For safety, let's write it as a generic error if it's not empty.
				// fmt.Fprintf(trw.ginWriter, "{\"error\":\"Unexpected stream data: %s\"}\n", line)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(trw.ginWriter, "{\"error\":\"Error scanning stream buffer: %v\"}\n", err)
	}

	// Preserve the remaining part of the buffer that wasn't a full line
	unprocessedSuffix = trw.buffer.Bytes()[trw.buffer.Len()-len(scanner.Bytes()):]
	trw.buffer.Reset()
	trw.buffer.Write(unprocessedSuffix)

	if processedBuffer.Len() > 0 {
		trw.ginWriter.Write(processedBuffer.Bytes())
	}
	if flusher, ok := trw.ginWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (pm *ProxyManager) ollamaChatHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ollamaReq OllamaChatRequest
		if err := c.ShouldBindJSON(&ollamaReq); err != nil {
			pm.sendOllamaError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err))
			return
		}

		if ollamaReq.Model == "" {
			pm.sendOllamaError(c, http.StatusBadRequest, "Model name is required.")
			return
		}

		pg, realModelName, err := pm.swapProcessGroup(ollamaReq.Model)
		if err != nil {
			pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Error selecting model process: %v", err))
			return
		}

		process, ok := pg.processes[realModelName]
		if !ok {
			pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Process for model %s not found in group %s", realModelName, pg.id))
			return
		}

		// Transform Ollama messages to OpenAI messages
		openAIMessages := ollamaMessagesToOpenAI(ollamaReq.Messages)

		// Use UseModelName if configured, otherwise use realModelName
		modelNameToUse := realModelName
		if pm.config.Models[realModelName].UseModelName != "" {
			modelNameToUse = pm.config.Models[realModelName].UseModelName
		}

		// Create OpenAI-compatible request body
		isStreaming := ollamaReq.Stream != nil && *ollamaReq.Stream
		openAIReqBodyBytes, err := createOpenAIRequestBody(modelNameToUse, openAIMessages, isStreaming, ollamaReq.Options)
		if err != nil {
			pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating OpenAI request: %v", err))
			return
		}

		// Create a new http.Request to be sent to the model process
		// The URL path here is nominal as ProxyRequest constructs its own target URL.
		// However, it's good practice to set it to what the underlying model might expect if it were a direct OpenAI call.
		proxyDestReq, err := http.NewRequestWithContext(c.Request.Context(), "POST", "/v1/chat/completions", bytes.NewBuffer(openAIReqBodyBytes))
		if err != nil {
			pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating internal request: %v", err))
			return
		}
		proxyDestReq.Header.Set("Content-Type", "application/json")
		proxyDestReq.Header.Set("Accept", "application/json, text/event-stream") // Accept SSE for streaming
		// Copy other relevant headers from original request if needed (e.g., Authorization, custom headers)
		// For now, keeping it simple.

		if isStreaming {
			c.Header("Content-Type", "application/x-ndjson")
			c.Header("Transfer-Encoding", "chunked")
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")

			// Use transformingResponseWriter to convert SSE to Ollama ndjson
			trw := newTransformingResponseWriter(c.Writer, ollamaReq.Model, true)
			process.ProxyRequest(trw, proxyDestReq)
			// Final flush for any remaining buffered data in trw
			trw.Flush()
		} else {
			recorder := httptest.NewRecorder()
			process.ProxyRequest(recorder, proxyDestReq)

			if recorder.Code != http.StatusOK {
				// Try to parse error from recorder and forward
				var openAIError struct {
					Error struct {
						Message string `json:"message"`
						Type    string `json:"type"`
					} `json:"error"`
				}
				if json.Unmarshal(recorder.Body.Bytes(), &openAIError) == nil && openAIError.Error.Message != "" {
					pm.sendOllamaError(c, recorder.Code, openAIError.Error.Message)
				} else {
					pm.sendOllamaError(c, recorder.Code, fmt.Sprintf("Upstream error: %s", recorder.Body.String()))
				}
				return
			}

			// Transform OpenAI non-streaming response to Ollama non-streaming response
			var openAIResp OpenAIChatCompletionResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &openAIResp); err != nil {
				pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Error parsing OpenAI response: %v. Body: %s", err, recorder.Body.String()))
				return
			}

			if len(openAIResp.Choices) == 0 {
				pm.sendOllamaError(c, http.StatusInternalServerError, "OpenAI response contained no choices.")
				return
			}

			choice := openAIResp.Choices[0]
			ollamaFinalResp := OllamaChatResponse{
				Model:     ollamaReq.Model, // Use the requested model name
				CreatedAt: time.Unix(openAIResp.Created, 0).UTC(),
				Message: OllamaMessage{
					Role:    openAIRoleToOllama(choice.Message.Role),
					Content: choice.Message.Content,
				},
				Done:            true,
				DoneReason:      openAIFinishReasonToOllama(choice.FinishReason),
				TotalDuration:   0, // Placeholder
				LoadDuration:    0, // Placeholder
				PromptEvalCount: openAIResp.Usage.PromptTokens,
				EvalCount:       openAIResp.Usage.CompletionTokens,
			}
			c.JSON(http.StatusOK, ollamaFinalResp)
		}
	}
}

func (pm *ProxyManager) ollamaGenerateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ollamaReq OllamaGenerateRequest
		if err := c.ShouldBindJSON(&ollamaReq); err != nil {
			pm.sendOllamaError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err))
			return
		}

		if ollamaReq.Model == "" {
			pm.sendOllamaError(c, http.StatusBadRequest, "Model name is required.")
			return
		}
		if ollamaReq.Raw {
			pm.sendOllamaError(c, http.StatusNotImplemented, "Raw mode for /api/generate is not implemented.")
			return
		}
		if len(ollamaReq.Images) > 0 {
			pm.sendOllamaError(c, http.StatusNotImplemented, "Image input for /api/generate is not implemented.")
			return
		}

		pg, realModelName, err := pm.swapProcessGroup(ollamaReq.Model)
		if err != nil {
			pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Error selecting model process: %v", err))
			return
		}

		process, ok := pg.processes[realModelName]
		if !ok {
			pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Process for model %s not found in group %s", realModelName, pg.id))
			return
		}

		// Use UseModelName if configured, otherwise use realModelName
		modelNameToUse := realModelName
		if pm.config.Models[realModelName].UseModelName != "" {
			modelNameToUse = pm.config.Models[realModelName].UseModelName
		}

		// Create OpenAI-compatible legacy completion request body
		isStreaming := ollamaReq.Stream != nil && *ollamaReq.Stream

		// Construct prompt for legacy completion
		// Ollama's /api/generate can take system and template, which are usually for chat.
		// For simplicity, we'll combine system and prompt if system is provided.
		// A more advanced implementation might convert this to a chat-like structure if the backend prefers.
		fullPrompt := ollamaReq.Prompt
		if ollamaReq.System != "" {
			fullPrompt = ollamaReq.System + "\n\n" + ollamaReq.Prompt // Basic concatenation
		}
		// Note: ollamaReq.Template is ignored for now as llama-swap doesn't handle templating.

		openAIReqBodyBytes, err := createOpenAILegacyCompletionRequestBody(modelNameToUse, fullPrompt, isStreaming, ollamaReq.Options)
		if err != nil {
			pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating OpenAI request: %v", err))
			return
		}

		proxyDestReq, err := http.NewRequestWithContext(c.Request.Context(), "POST", "/v1/completions", bytes.NewBuffer(openAIReqBodyBytes))
		if err != nil {
			pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating internal request: %v", err))
			return
		}
		proxyDestReq.Header.Set("Content-Type", "application/json")
		proxyDestReq.Header.Set("Accept", "application/json, text/event-stream")

		if isStreaming {
			c.Header("Content-Type", "application/x-ndjson")
			c.Header("Transfer-Encoding", "chunked")
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")

			trw := newTransformingResponseWriter(c.Writer, ollamaReq.Model, false) // false for isChat
			process.ProxyRequest(trw, proxyDestReq)
			trw.Flush() // Final flush
		} else {
			recorder := httptest.NewRecorder()
			process.ProxyRequest(recorder, proxyDestReq)

			if recorder.Code != http.StatusOK {
				var openAIError struct {
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if json.Unmarshal(recorder.Body.Bytes(), &openAIError) == nil && openAIError.Error.Message != "" {
					pm.sendOllamaError(c, recorder.Code, openAIError.Error.Message)
				} else {
					pm.sendOllamaError(c, recorder.Code, fmt.Sprintf("Upstream error: %s", recorder.Body.String()))
				}
				return
			}

			var openAIResp OpenAICompletionResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &openAIResp); err != nil {
				pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Error parsing OpenAI response: %v. Body: %s", err, recorder.Body.String()))
				return
			}

			if len(openAIResp.Choices) == 0 {
				pm.sendOllamaError(c, http.StatusInternalServerError, "OpenAI response contained no choices.")
				return
			}

			choice := openAIResp.Choices[0]
			ollamaFinalResp := OllamaGenerateResponse{
				Model:      ollamaReq.Model,
				CreatedAt:  time.Unix(openAIResp.Created, 0).UTC(),
				Response:   choice.Text,
				Done:       true,
				DoneReason: openAIFinishReasonToOllama(choice.FinishReason),
				// Context: Not directly available from OpenAI legacy completion in this way
				PromptEvalCount: openAIResp.Usage.PromptTokens,
				EvalCount:       openAIResp.Usage.CompletionTokens,
			}
			c.JSON(http.StatusOK, ollamaFinalResp)
		}
	}
}

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
