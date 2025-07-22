package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// MetricsMiddleware sets up the MetricsResponseWriter for capturing upstream requests
func MetricsMiddleware(pm *ProxyManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pm.sendErrorResponse(c, http.StatusBadRequest, "could not ready request body")
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		requestedModel := gjson.GetBytes(bodyBytes, "model").String()
		if requestedModel == "" {
			pm.sendErrorResponse(c, http.StatusBadRequest, "missing or invalid 'model' key")
			return
		}

		realModelName, found := pm.config.RealModelName(requestedModel)
		if !found {
			pm.sendErrorResponse(c, http.StatusBadRequest, fmt.Sprintf("could not find real modelID for %s", requestedModel))
			return
		}
		c.Set("ls-real-model-name", realModelName)

		writer := &MetricsResponseWriter{
			ResponseWriter: c.Writer,
			metricsRecorder: &MetricsRecorder{
				metricsMonitor: pm.metricsMonitor,
				realModelName:  realModelName,
				isStreaming:    gjson.GetBytes(bodyBytes, "stream").Bool(),
				startTime:      time.Now(),
			},
		}
		c.Writer = writer
		c.Next()

		rec := writer.metricsRecorder
		rec.processBody(writer.body)
	}
}

type MetricsRecorder struct {
	metricsMonitor *MetricsMonitor
	realModelName  string
	isStreaming    bool
	startTime      time.Time
}

// processBody handles response processing after request completes
func (rec *MetricsRecorder) processBody(body []byte) {
	if rec.isStreaming {
		rec.processStreamingResponse(body)
	} else {
		rec.processNonStreamingResponse(body)
	}
}

func (rec *MetricsRecorder) parseAndRecordMetrics(jsonData gjson.Result) {
	if !jsonData.Get("usage").Exists() {
		return
	}

	outputTokens := int(jsonData.Get("usage.completion_tokens").Int())
	inputTokens := int(jsonData.Get("usage.prompt_tokens").Int())

	if outputTokens > 0 {
		duration := time.Since(rec.startTime)
		tokensPerSecond := float64(inputTokens+outputTokens) / duration.Seconds()

		metrics := TokenMetrics{
			Timestamp:       time.Now(),
			Model:           rec.realModelName,
			InputTokens:     inputTokens,
			OutputTokens:    outputTokens,
			TokensPerSecond: tokensPerSecond,
			DurationMs:      int(duration.Milliseconds()),
		}
		rec.metricsMonitor.addMetrics(metrics)
	}
}

func (rec *MetricsRecorder) processStreamingResponse(body []byte) {
	lines := bytes.Split(body, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Check for SSE data prefix
		if data, found := bytes.CutPrefix(line, []byte("data:")); found {
			data = bytes.TrimSpace(data)
			if len(data) == 0 {
				continue
			}
			if bytes.Equal(data, []byte("[DONE]")) {
				break
			}

			// Parse JSON to look for usage data
			if gjson.ValidBytes(data) {
				rec.parseAndRecordMetrics(gjson.ParseBytes(data))
			}
		}
	}
}

func (rec *MetricsRecorder) processNonStreamingResponse(body []byte) {
	if len(body) == 0 {
		return
	}

	// Parse JSON to extract usage information
	if gjson.ValidBytes(body) {
		rec.parseAndRecordMetrics(gjson.ParseBytes(body))
	}
}

// MetricsResponseWriter captures the entire response for non-streaming
type MetricsResponseWriter struct {
	gin.ResponseWriter
	body            []byte
	metricsRecorder *MetricsRecorder
}

func (w *MetricsResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	if err != nil {
		return n, err
	}
	w.body = append(w.body, b...)
	return n, nil
}

func (w *MetricsResponseWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *MetricsResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}
