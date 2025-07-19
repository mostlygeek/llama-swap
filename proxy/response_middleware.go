package proxy

import (
	"bytes"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ResponseMiddlewareConfig holds configuration for the response middleware
type ResponseMiddlewareConfig struct {
	MetricsParser   *MetricsParser
	Logger          *LogMonitor
	ModelName       string
	StartTime       time.Time
	IsStreaming     bool
	ParseForMetrics bool
}

// ResponseData holds captured response information
type ResponseData struct {
	StatusCode int
	Header     map[string][]string
	Body       []byte
}

// ResponseMiddleware is a gin middleware that captures and processes responses
// It replaces both ResponseRecorder and StreamingResponseRecorder classes
func ResponseMiddleware(config ResponseMiddlewareConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip processing if metrics parsing is disabled
		if !config.ParseForMetrics || config.MetricsParser == nil {
			c.Next()
			return
		}

		// Create response data holder
		responseData := &ResponseData{
			StatusCode: http.StatusOK,
			Header:     make(map[string][]string),
		}

		// Create custom response writer based on streaming mode
		if config.IsStreaming {
			// Handle streaming responses
			writer := &streamingResponseWriter{
				ResponseWriter: c.Writer,
				config:         config,
				responseData:   responseData,
				buffer:         make([]byte, 0),
			}
			c.Writer = writer
		} else {
			// Handle non-streaming responses
			writer := &bufferingResponseWriter{
				ResponseWriter: c.Writer,
				responseData:   responseData,
			}
			c.Writer = writer
		}

		// Process the request
		c.Next()

		// Handle non-streaming response processing after request completes
		if !config.IsStreaming && len(responseData.Body) > 0 {
			processNonStreamingResponse(config, responseData)
		}
	}
}

// streamingResponseWriter handles streaming responses (SSE)
type streamingResponseWriter struct {
	gin.ResponseWriter
	config       ResponseMiddlewareConfig
	responseData *ResponseData
	buffer       []byte
}

func (w *streamingResponseWriter) Write(b []byte) (int, error) {
	// Write to the actual response writer first
	n, err := w.ResponseWriter.Write(b)
	if err != nil {
		return n, err
	}

	// Append to buffer for parsing
	w.buffer = append(w.buffer, b...)

	// Process the buffer for complete SSE events
	w.processBuffer()

	return n, nil
}

func (w *streamingResponseWriter) processBuffer() {
	// Process each line as potential SSE data
	lines := bytes.Split(w.buffer, []byte("\n"))

	// Keep incomplete line in buffer
	if len(lines) > 0 && !bytes.HasSuffix(w.buffer, []byte("\n")) {
		w.buffer = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	} else {
		w.buffer = nil
	}

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Check for SSE data prefix
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimSpace(line[6:])

			// Skip SSE comments and empty data
			if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
				continue
			}

			// Parse JSON to look for usage data
			if gjson.ValidBytes(data) {
				jsonData := gjson.ParseBytes(data)

				// Check if this chunk contains usage information
				if jsonData.Get("usage").Exists() {
					outputTokens := int(jsonData.Get("usage.completion_tokens").Int())
					inputTokens := int(jsonData.Get("usage.prompt_tokens").Int())

					if outputTokens > 0 {
						duration := time.Since(w.config.StartTime)
						generationSpeed := float64(inputTokens+outputTokens) / duration.Seconds()

						metrics := TokenMetrics{
							Timestamp:       time.Now(),
							Model:           w.config.ModelName,
							InputTokens:     inputTokens,
							OutputTokens:    outputTokens,
							TokensPerSecond: generationSpeed,
							DurationMs:      int(duration.Milliseconds()),
						}
						w.config.MetricsParser.addMetrics(metrics)
					}
				}
			}
		}
	}
}

// bufferingResponseWriter captures the entire response for non-streaming
type bufferingResponseWriter struct {
	gin.ResponseWriter
	responseData *ResponseData
}

func (w *bufferingResponseWriter) Write(b []byte) (int, error) {
	// Capture the response data
	w.responseData.Body = append(w.responseData.Body, b...)
	return w.ResponseWriter.Write(b)
}

func (w *bufferingResponseWriter) WriteHeader(statusCode int) {
	w.responseData.StatusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *bufferingResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

// processNonStreamingResponse processes metrics for non-streaming responses
func processNonStreamingResponse(config ResponseMiddlewareConfig, responseData *ResponseData) {
	if len(responseData.Body) == 0 {
		return
	}

	// Parse JSON to extract usage information
	if gjson.ValidBytes(responseData.Body) {
		jsonData := gjson.ParseBytes(responseData.Body)

		// Check if response contains usage information
		if jsonData.Get("usage").Exists() {
			outputTokens := int(jsonData.Get("usage.completion_tokens").Int())
			inputTokens := int(jsonData.Get("usage.prompt_tokens").Int())

			if outputTokens > 0 {
				duration := time.Since(config.StartTime)
				generationSpeed := float64(inputTokens+outputTokens) / duration.Seconds()

				metrics := TokenMetrics{
					Timestamp:       time.Now(),
					Model:           config.ModelName,
					InputTokens:     inputTokens,
					OutputTokens:    outputTokens,
					TokensPerSecond: generationSpeed,
					DurationMs:      int(duration.Milliseconds()),
				}
				config.MetricsParser.addMetrics(metrics)
			}
		}
	}
}
