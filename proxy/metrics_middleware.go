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
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		requestedModel := gjson.GetBytes(bodyBytes, "model").String()
		if requestedModel == "" {
			pm.sendErrorResponse(c, http.StatusBadRequest, "missing or invalid 'model' key")
			c.Abort()
			return
		}

		realModelName, found := pm.config.RealModelName(requestedModel)
		if !found {
			pm.sendErrorResponse(c, http.StatusBadRequest, fmt.Sprintf("could not find real modelID for %s", requestedModel))
			c.Abort()
			return
		}

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

func (rec *MetricsRecorder) parseAndRecordMetrics(jsonData gjson.Result) bool {
	usage := jsonData.Get("usage")
	if !usage.Exists() {
		return false
	}

	// default values
	outputTokens := int(jsonData.Get("usage.completion_tokens").Int())
	inputTokens := int(jsonData.Get("usage.prompt_tokens").Int())
	tokensPerSecond := -1.0
	promptPerSecond := -1.0
	durationMs := int(time.Since(rec.startTime).Milliseconds())

	// use llama-server's timing data for tok/sec and duration as it is more accurate
	if timings := jsonData.Get("timings"); timings.Exists() {
		promptPerSecond = jsonData.Get("timings.prompt_per_second").Float()
		tokensPerSecond = jsonData.Get("timings.predicted_per_second").Float()
		durationMs = int(jsonData.Get("timings.prompt_ms").Float() + jsonData.Get("timings.predicted_ms").Float())
	}

	rec.metricsMonitor.addMetrics(TokenMetrics{
		Timestamp:       time.Now(),
		Model:           rec.realModelName,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		PromptPerSecond: promptPerSecond,
		TokensPerSecond: tokensPerSecond,
		DurationMs:      durationMs,
	})

	return true
}

func (rec *MetricsRecorder) processStreamingResponse(body []byte) {
	// Iterate **backwards** through the lines looking for the data payload with
	// usage data
	lines := bytes.Split(body, []byte("\n"))

	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}

		// SSE payload always follows "data:"
		prefix := []byte("data:")
		if !bytes.HasPrefix(line, prefix) {
			continue
		}
		data := bytes.TrimSpace(line[len(prefix):])

		if len(data) == 0 {
			continue
		}

		if bytes.Equal(data, []byte("[DONE]")) {
			// [DONE] line itself contains nothing of interest.
			continue
		}

		if gjson.ValidBytes(data) {
			if rec.parseAndRecordMetrics(gjson.ParseBytes(data)) {
				return // short circuit if a metric was recorded
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
