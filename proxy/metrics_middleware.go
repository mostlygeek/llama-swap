package proxy

import (
	"bytes"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// MetricsMiddlewareConfig holds configuration for the response middleware
type MetricsMiddlewareConfig struct {
	MetricsParser   *MetricsMonitor
	ModelName       string
	StartTime       time.Time
	IsStreaming     bool
	ParseForMetrics bool
}

// parseAndRecordMetrics parses usage data from JSON and records metrics
func (config *MetricsMiddlewareConfig) parseAndRecordMetrics(jsonData gjson.Result) {
	if !jsonData.Get("usage").Exists() {
		return
	}

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

// MetricsMiddleware is a gin middleware that captures and processes responses
func MetricsMiddleware(config MetricsMiddlewareConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip processing if metrics parsing is disabled
		if !config.ParseForMetrics || config.MetricsParser == nil {
			c.Next()
			return
		}

		// Create response recorder for non-streaming
		if !config.IsStreaming {
			c.Writer = &bufferingResponseWriter{
				ResponseWriter: c.Writer,
			}
		} else {
			c.Writer = &streamingResponseWriter{
				ResponseWriter: c.Writer,
				config:         config,
			}
		}

		// Process the request
		c.Next()

		// Handle non-streaming response processing after request completes
		if !config.IsStreaming {
			if writer, ok := c.Writer.(*bufferingResponseWriter); ok && len(writer.body) > 0 {
				processNonStreamingResponse(config, writer.body)
			}
		}
	}
}

// streamingResponseWriter handles streaming responses (SSE)
type streamingResponseWriter struct {
	gin.ResponseWriter
	config MetricsMiddlewareConfig
	buffer []byte
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
				w.config.parseAndRecordMetrics(jsonData)
			}
		}
	}
}

// bufferingResponseWriter captures the entire response for non-streaming
type bufferingResponseWriter struct {
	gin.ResponseWriter
	body []byte
}

func (w *bufferingResponseWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return w.ResponseWriter.Write(b)
}

func (w *bufferingResponseWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *bufferingResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

// processNonStreamingResponse processes metrics for non-streaming responses
func processNonStreamingResponse(config MetricsMiddlewareConfig, body []byte) {
	if len(body) == 0 {
		return
	}

	// Parse JSON to extract usage information
	if gjson.ValidBytes(body) {
		jsonData := gjson.ParseBytes(body)
		config.parseAndRecordMetrics(jsonData)
	}
}
