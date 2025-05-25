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
			details := OllamaModelDetails{Format: "gguf"}
			if modelCfg.Metadata.Family != "" {
				details.Family = modelCfg.Metadata.Family
			} else {
				// Basic inference for list view
				arch := "unknown"
				if modelCfg.Metadata.Architecture != "" {
					arch = modelCfg.Metadata.Architecture
				} else {
					arch = inferPattern(id, architecturePatterns, orderedArchKeys)
				}
				details.Family = inferFamilyFromName(id, arch)
			}
			if modelCfg.Metadata.ParameterSize != "" {
				details.ParameterSize = modelCfg.Metadata.ParameterSize
			} else {
				details.ParameterSize = inferParameterSizeFromName(id)
			}
			if modelCfg.Metadata.QuantizationLevel != "" {
				details.QuantizationLevel = modelCfg.Metadata.QuantizationLevel
			} else {
				details.QuantizationLevel = inferQuantizationLevelFromName(id)
			}
			if details.Family != "unknown" && details.Family != "" {
				details.Families = []string{details.Family}
			}

			models = append(models, OllamaModelResponse{
				Name:       id,
				Model:      id,
				ModifiedAt: now,
				Size:       0,
				Digest:     fmt.Sprintf("%x", id),
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
			modelName = req.Name
		}

		if modelName == "" {
			pm.sendOllamaError(c, http.StatusBadRequest, "Model name is required.")
			return
		}

		pm.RLock()
		modelCfg, id, found := pm.config.FindConfig(modelName) // id is realModelName
		pm.RUnlock()

		if !found {
			pm.sendOllamaError(c, http.StatusNotFound, fmt.Sprintf("Model '%s' not found.", modelName))
			return
		}

		parser := NewLlamaServerParser()
		parsedArgs := parser.Parse(modelCfg.Cmd, id)

		arch := parsedArgs.Architecture
		family := parsedArgs.Family
		paramSize := parsedArgs.ParameterSize
		quantLevel := parsedArgs.QuantizationLevel
		ctxLength := parsedArgs.ContextLength
		caps := parsedArgs.Capabilities
		if len(caps) == 0 {
			caps = []string{"completion"}
		}

		// Override with MetadataConfig if present
		if modelCfg.Metadata.Architecture != "" {
			arch = modelCfg.Metadata.Architecture
		}
		if modelCfg.Metadata.Family != "" {
			family = modelCfg.Metadata.Family
		}
		if modelCfg.Metadata.ParameterSize != "" {
			paramSize = modelCfg.Metadata.ParameterSize
		}
		if modelCfg.Metadata.QuantizationLevel != "" {
			quantLevel = modelCfg.Metadata.QuantizationLevel
		}
		if modelCfg.Metadata.ContextLength > 0 {
			ctxLength = modelCfg.Metadata.ContextLength
		}
		if len(modelCfg.Metadata.Capabilities) > 0 {
			caps = modelCfg.Metadata.Capabilities
		}

		details := OllamaModelDetails{
			Format:            "gguf",
			Family:            family,
			ParameterSize:     paramSize,
			QuantizationLevel: quantLevel,
		}
		if family != "unknown" && family != "" {
			details.Families = []string{family}
		}

		modelInfo := map[string]interface{}{
			"general.architecture": arch,
		}
		if ctxLength > 0 {
			modelInfo["llama.context_length"] = ctxLength
		} else {
			modelInfo["llama.context_length"] = 2048
		}

		resp := OllamaShowResponse{
			Details:      details,
			ModelInfo:    modelInfo,
			Capabilities: caps,
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
					expiresAt := time.Time{} // Zero time if no TTL
					if process.config.UnloadAfter > 0 {
						expiresAt = process.lastRequestHandled.Add(time.Duration(process.config.UnloadAfter) * time.Second)
						if expiresAt.Before(now) && !process.lastRequestHandled.IsZero() {
							expiresAt = now.Add(time.Duration(process.config.UnloadAfter) * time.Second)
						} else if process.lastRequestHandled.IsZero() {
							expiresAt = now.Add(time.Duration(process.config.UnloadAfter) * time.Second)
						}
					}

					modelCfg := process.config
					details := OllamaModelDetails{Format: "gguf"}

					arch := "unknown"
					if modelCfg.Metadata.Architecture != "" {
						arch = modelCfg.Metadata.Architecture
					} else {
						arch = inferPattern(modelID, architecturePatterns, orderedArchKeys)
					}

					if modelCfg.Metadata.Family != "" {
						details.Family = modelCfg.Metadata.Family
					} else {
						details.Family = inferFamilyFromName(modelID, arch)
					}
					if modelCfg.Metadata.ParameterSize != "" {
						details.ParameterSize = modelCfg.Metadata.ParameterSize
					} else {
						details.ParameterSize = inferParameterSizeFromName(modelID)
					}
					if modelCfg.Metadata.QuantizationLevel != "" {
						details.QuantizationLevel = modelCfg.Metadata.QuantizationLevel
					} else {
						details.QuantizationLevel = inferQuantizationLevelFromName(modelID)
					}
					if details.Family != "unknown" && details.Family != "" {
						details.Families = []string{details.Family}
					}

					runningModels = append(runningModels, OllamaProcessModelResponse{
						Name:      modelID,
						Model:     modelID,
						Size:      0,
						Digest:    fmt.Sprintf("%x", modelID),
						Details:   details,
						ExpiresAt: expiresAt,
						SizeVRAM:  0,
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
				break
			}

			var ollamaChunkJSON []byte
			var err error

			if trw.isChat {
				var openAIChatChunk OpenAIStreamingChatResponse
				if err = json.Unmarshal([]byte(jsonData), &openAIChatChunk); err == nil {
					if len(openAIChatChunk.Choices) > 0 {
						choice := openAIChatChunk.Choices[0]
						ollamaResp := OllamaChatResponse{
							Model:     trw.modelName,
							CreatedAt: time.Now().UTC(),
							Message: OllamaMessage{
								Role:    openAIRoleToOllama(choice.Delta.Role),
								Content: choice.Delta.Content,
							},
							Done:       choice.FinishReason != "",
							DoneReason: openAIFinishReasonToOllama(choice.FinishReason),
						}
						if choice.Delta.Role == "" && ollamaResp.Message.Role == "" {
							ollamaResp.Message.Role = "assistant"
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
							Model:      trw.modelName,
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
				fmt.Fprintf(trw.ginWriter, "{\"error\":\"Error transforming stream: %v\"}\n", err)
			}
		} else if line != "" {
			var errResp OllamaErrorResponse
			if json.Unmarshal([]byte(line), &errResp) == nil && errResp.Error != "" {
				processedBuffer.Write([]byte(line))
				processedBuffer.WriteString("\n")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(trw.ginWriter, "{\"error\":\"Error scanning stream buffer: %v\"}\n", err)
	}

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

		openAIMessages := ollamaMessagesToOpenAI(ollamaReq.Messages)
		modelNameToUse := realModelName
		if pm.config.Models[realModelName].UseModelName != "" {
			modelNameToUse = pm.config.Models[realModelName].UseModelName
		}

		isStreaming := ollamaReq.Stream != nil && *ollamaReq.Stream
		openAIReqBodyBytes, err := createOpenAIRequestBody(modelNameToUse, openAIMessages, isStreaming, ollamaReq.Options)
		if err != nil {
			pm.sendOllamaError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating OpenAI request: %v", err))
			return
		}

		proxyDestReq, err := http.NewRequestWithContext(c.Request.Context(), "POST", "/v1/chat/completions", bytes.NewBuffer(openAIReqBodyBytes))
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

			trw := newTransformingResponseWriter(c.Writer, ollamaReq.Model, true)
			process.ProxyRequest(trw, proxyDestReq)
			trw.Flush()
		} else {
			recorder := httptest.NewRecorder()
			process.ProxyRequest(recorder, proxyDestReq)

			if recorder.Code != http.StatusOK {
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
				Model:     ollamaReq.Model,
				CreatedAt: time.Unix(openAIResp.Created, 0).UTC(),
				Message: OllamaMessage{
					Role:    openAIRoleToOllama(choice.Message.Role),
					Content: choice.Message.Content,
				},
				Done:            true,
				DoneReason:      openAIFinishReasonToOllama(choice.FinishReason),
				TotalDuration:   0,
				LoadDuration:    0,
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

		modelNameToUse := realModelName
		if pm.config.Models[realModelName].UseModelName != "" {
			modelNameToUse = pm.config.Models[realModelName].UseModelName
		}

		isStreaming := ollamaReq.Stream != nil && *ollamaReq.Stream
		fullPrompt := ollamaReq.Prompt
		if ollamaReq.System != "" {
			fullPrompt = ollamaReq.System + "\n\n" + ollamaReq.Prompt
		}

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

			trw := newTransformingResponseWriter(c.Writer, ollamaReq.Model, false)
			process.ProxyRequest(trw, proxyDestReq)
			trw.Flush()
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
				Model:           ollamaReq.Model,
				CreatedAt:       time.Unix(openAIResp.Created, 0).UTC(),
				Response:        choice.Text,
				Done:            true,
				DoneReason:      openAIFinishReasonToOllama(choice.FinishReason),
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
	Format    string                 `json:"format,omitempty"`
	Images    []string               `json:"images,omitempty"`
	KeepAlive string                 `json:"keep_alive,omitempty"`
	Options   map[string]interface{} `json:"options,omitempty"`
}

// OllamaGenerateResponse is the response from /api/generate.
type OllamaGenerateResponse struct {
	Model              string    `json:"model"`
	CreatedAt          time.Time `json:"created_at"`
	Response           string    `json:"response,omitempty"`
	Done               bool      `json:"done"`
	DoneReason         string    `json:"done_reason,omitempty"`
	Context            []int     `json:"context,omitempty"`
	TotalDuration      int64     `json:"total_duration,omitempty"`
	LoadDuration       int64     `json:"load_duration,omitempty"`
	PromptEvalCount    int       `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64     `json:"prompt_eval_duration,omitempty"`
	EvalCount          int       `json:"eval_count,omitempty"`
	EvalDuration       int64     `json:"eval_duration,omitempty"`
}

// OllamaMessage represents a single message in a chat.
type OllamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

// OllamaChatRequest describes a request to /api/chat.
type OllamaChatRequest struct {
	Model     string                 `json:"model"`
	Messages  []OllamaMessage        `json:"messages"`
	Stream    *bool                  `json:"stream,omitempty"`
	Format    string                 `json:"format,omitempty"`
	KeepAlive string                 `json:"keep_alive,omitempty"`
	Options   map[string]interface{} `json:"options,omitempty"`
}

// OllamaChatResponse is the response from /api/chat.
type OllamaChatResponse struct {
	Model              string        `json:"model"`
	CreatedAt          time.Time     `json:"created_at"`
	Message            OllamaMessage `json:"message,omitempty"`
	Done               bool          `json:"done"`
	DoneReason         string        `json:"done_reason,omitempty"`
	TotalDuration      int64         `json:"total_duration,omitempty"`
	LoadDuration       int64         `json:"load_duration,omitempty"`
	PromptEvalCount    int           `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64         `json:"prompt_eval_duration,omitempty"`
	EvalCount          int           `json:"eval_count,omitempty"`
	EvalDuration       int64         `json:"eval_duration,omitempty"`
}

// OllamaListTagsResponse is the response from /api/tags.
type OllamaListTagsResponse struct {
	Models []OllamaModelResponse `json:"models"`
}

// OllamaModelResponse describes a single model in the list.
type OllamaModelResponse struct {
	Name       string             `json:"name"`
	Model      string             `json:"model"`
	ModifiedAt time.Time          `json:"modified_at"`
	Size       int64              `json:"size"`
	Digest     string             `json:"digest"`
	Details    OllamaModelDetails `json:"details"`
}

// OllamaModelDetails provides more details about a model.
type OllamaModelDetails struct {
	ParentModel       string   `json:"parent_model,omitempty"`
	Format            string   `json:"format,omitempty"`
	Family            string   `json:"family,omitempty"`
	Families          []string `json:"families,omitempty"`
	ParameterSize     string   `json:"parameter_size,omitempty"`
	QuantizationLevel string   `json:"quantization_level,omitempty"`
}

type OllamaTensor struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Shape []uint64 `json:"shape"`
}

// OllamaShowRequest is the request for /api/show.
type OllamaShowRequest struct {
	Model string `json:"model,omitempty"`
	Name  string `json:"name,omitempty"`
}

// OllamaShowResponse is the response from /api/show.
type OllamaShowResponse struct {
	License       string             `json:"license,omitempty"`
	Modelfile     string             `json:"modelfile,omitempty"`
	Parameters    string             `json:"parameters,omitempty"`
	Template      string             `json:"template,omitempty"`
	System        string             `json:"system,omitempty"`
	Details       OllamaModelDetails `json:"details,omitempty"`
	Messages      []OllamaMessage    `json:"messages,omitempty"`
	ModelInfo     map[string]any     `json:"model_info,omitempty"`
	ProjectorInfo map[string]any     `json:"projector_info,omitempty"`
	Tensors       []OllamaTensor     `json:"tensors,omitempty"`
	Capabilities  []string           `json:"capabilities,omitempty"`
	ModifiedAt    time.Time          `json:"modified_at,omitempty"`
}

// OllamaProcessResponse is the response from /api/ps.
type OllamaProcessResponse struct {
	Models []OllamaProcessModelResponse `json:"models"`
}

// OllamaProcessModelResponse describes a running model process.
type OllamaProcessModelResponse struct {
	Name      string             `json:"name"`
	Model     string             `json:"model"`
	Size      int64              `json:"size"`
	Digest    string             `json:"digest"`
	Details   OllamaModelDetails `json:"details"`
	ExpiresAt time.Time          `json:"expires_at"`
	SizeVRAM  int64              `json:"size_vram"`
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
	Usage   *OpenAIUsage                       `json:"usage,omitempty"`
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
	Usage   *OpenAIUsage                   `json:"usage,omitempty"`
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
	Choices []OpenAICompletionStreamChoice `json:"choices"`
	Usage   OpenAIUsage                    `json:"usage"`
}

func openAIFinishReasonToOllama(reason string) string {
	switch reason {
	case "stop":
		return "stop"
	case "length":
		return "length"
	case "content_filter":
		return "content_filter"
	case "tool_calls":
		return "tool_calls"
	default:
		if reason != "" {
			return "unknown"
		}
		return ""
	}
}

func openAIRoleToOllama(role string) string {
	switch role {
	case "system":
		return "system"
	case "user":
		return "user"
	case "assistant":
		return "assistant"
	default:
		return role
	}
}

func ollamaMessagesToOpenAI(ollamaMsgs []OllamaMessage) []map[string]interface{} {
	openAIMsgs := make([]map[string]interface{}, len(ollamaMsgs))
	for i, msg := range ollamaMsgs {
		openAIMsgs[i] = map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
	}
	return openAIMsgs
}

func createOpenAIRequestBody(modelName string, messages []map[string]interface{}, stream bool, options map[string]interface{}) ([]byte, error) {
	requestBody := map[string]interface{}{
		"model":    modelName,
		"messages": messages,
		"stream":   stream,
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
