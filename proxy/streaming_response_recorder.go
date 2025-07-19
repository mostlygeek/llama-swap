package proxy

import (
	"bytes"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// StreamingResponseRecorder wraps gin.ResponseWriter to capture and parse streaming responses
type StreamingResponseRecorder struct {
	gin.ResponseWriter
	modelName     string
	startTime     time.Time
	metricsParser *MetricsParser
	proxyLogger   *LogMonitor
	buffer        []byte
}

func (w *StreamingResponseRecorder) Write(b []byte) (int, error) {
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

func (w *StreamingResponseRecorder) processBuffer() {
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
						duration := time.Since(w.startTime)
						generationSpeed := float64(inputTokens+outputTokens) / duration.Seconds()

						metrics := TokenMetrics{
							Timestamp:       time.Now(),
							Model:           w.modelName,
							InputTokens:     inputTokens,
							OutputTokens:    outputTokens,
							TokensPerSecond: generationSpeed,
							DurationMs:      int(duration.Milliseconds()),
						}
						w.metricsParser.addMetrics(metrics)
					}
				}
			}
		}
	}
}
