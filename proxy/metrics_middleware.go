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
	MetricsParser *MetricsMonitor
	ModelName     string
	IsStreaming   bool
	StartTime     time.Time
}

// MetricsMiddleware is a gin middleware that captures and processes responses
func MetricsMiddleware(config MetricsMiddlewareConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		config.StartTime = time.Now()

		// Pass the request to upstream
		writer := &MetricsResponseWriter{
			ResponseWriter: c.Writer,
		}
		c.Writer = writer
		c.Next()

		// Handle response processing after request completes
		if config.IsStreaming {
			config.processStreamingResponse(writer.body)
		} else {
			config.processNonStreamingResponse(writer.body)
		}
	}
}

func (config *MetricsMiddlewareConfig) parseAndRecordMetrics(jsonData gjson.Result) {
	if !jsonData.Get("usage").Exists() {
		return
	}

	outputTokens := int(jsonData.Get("usage.completion_tokens").Int())
	inputTokens := int(jsonData.Get("usage.prompt_tokens").Int())

	if outputTokens > 0 {
		duration := time.Since(config.StartTime)
		tokensPerSecond := float64(inputTokens+outputTokens) / duration.Seconds()

		metrics := TokenMetrics{
			Timestamp:       time.Now(),
			Model:           config.ModelName,
			InputTokens:     inputTokens,
			OutputTokens:    outputTokens,
			TokensPerSecond: tokensPerSecond,
			DurationMs:      int(duration.Milliseconds()),
		}
		config.MetricsParser.addMetrics(metrics)
	}
}

func (config *MetricsMiddlewareConfig) processStreamingResponse(body []byte) {
	lines := bytes.Split(body, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Check for SSE data prefix
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimSpace(line[6:])
			if len(data) == 0 {
				continue
			}
			if bytes.Equal(data, []byte("[DONE]")) {
				break
			}

			// Parse JSON to look for usage data
			if gjson.ValidBytes(data) {
				config.parseAndRecordMetrics(gjson.ParseBytes(data))
			}
		}
	}
}

func (config *MetricsMiddlewareConfig) processNonStreamingResponse(body []byte) {
	if len(body) == 0 {
		return
	}

	// Parse JSON to extract usage information
	if gjson.ValidBytes(body) {
		config.parseAndRecordMetrics(gjson.ParseBytes(body))
	}
}

// MetricsResponseWriter captures the entire response for non-streaming
type MetricsResponseWriter struct {
	gin.ResponseWriter
	body []byte
}

func (w *MetricsResponseWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return w.ResponseWriter.Write(b)
}

func (w *MetricsResponseWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *MetricsResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}
