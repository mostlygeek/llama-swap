package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/tidwall/gjson"
)

// TokenMetrics represents parsed token statistics from llama-server logs
type TokenMetrics struct {
	ID              int       `json:"id"`
	Timestamp       time.Time `json:"timestamp"`
	Model           string    `json:"model"`
	CachedTokens    int       `json:"cache_tokens"`
	InputTokens     int       `json:"input_tokens"`
	OutputTokens    int       `json:"output_tokens"`
	PromptPerSecond float64   `json:"prompt_per_second"`
	TokensPerSecond float64   `json:"tokens_per_second"`
	DurationMs      int       `json:"duration_ms"`
}

// TokenMetricsEvent represents a token metrics event
type TokenMetricsEvent struct {
	Metrics TokenMetrics
}

func (e TokenMetricsEvent) Type() uint32 {
	return TokenMetricsEventID // defined in events.go
}

// MetricsMonitor parses llama-server output for token statistics
type MetricsMonitor struct {
	mu         sync.RWMutex
	metrics    []TokenMetrics
	maxMetrics int
	nextID     int
}

func NewMetricsMonitor(config *config.Config) *MetricsMonitor {
	maxMetrics := config.MetricsMaxInMemory
	if maxMetrics <= 0 {
		maxMetrics = 1000 // Default fallback
	}

	mp := &MetricsMonitor{
		maxMetrics: maxMetrics,
	}

	return mp
}

// addMetrics adds a new metric to the collection and publishes an event
func (mp *MetricsMonitor) addMetrics(metric TokenMetrics) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	metric.ID = mp.nextID
	mp.nextID++
	mp.metrics = append(mp.metrics, metric)
	if len(mp.metrics) > mp.maxMetrics {
		mp.metrics = mp.metrics[len(mp.metrics)-mp.maxMetrics:]
	}
	event.Emit(TokenMetricsEvent{Metrics: metric})
}

// GetMetrics returns a copy of the current metrics
func (mp *MetricsMonitor) GetMetrics() []TokenMetrics {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	result := make([]TokenMetrics, len(mp.metrics))
	copy(result, mp.metrics)
	return result
}

// GetMetricsJSON returns metrics as JSON
func (mp *MetricsMonitor) GetMetricsJSON() ([]byte, error) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return json.Marshal(mp.metrics)
}

func (mp *MetricsMonitor) WrapHandler(
	modelID string,
	writer gin.ResponseWriter,
	request *http.Request,
	next func(modelID string, w http.ResponseWriter, r *http.Request) error,
) error {
	recorder := NewMetricsBodyRecorder(writer)
	var err error
	var tm TokenMetrics

	if err = next(modelID, recorder, request); err != nil {
		return err
	}

	if recorder.Status() != http.StatusOK {
		return nil
	}

	body := recorder.body.Bytes()
	if len(body) == 0 {
		return nil
	}

	if strings.Contains(recorder.Header().Get("Content-Type"), "text/event-stream") {
		tm, err = processStreamingResponse(modelID, recorder.StartTime(), body)
	} else {
		if gjson.ValidBytes(body) {
			tm, err = parseMetrics(modelID, recorder.StartTime(), gjson.ParseBytes(body)) //
		} else {
			err = fmt.Errorf("invalid JSON in response body")
		}
	}

	if err != nil {
		return err
	}

	mp.addMetrics(tm)
	return nil
}

func processStreamingResponse(modelID string, start time.Time, body []byte) (TokenMetrics, error) {
	// Iterate **backwards** through the body looking for the data payload with
	// usage data. This avoids allocating a slice of all lines via bytes.Split.

	// Start from the end of the body and scan backwards for newlines
	pos := len(body)
	for pos > 0 {
		// Find the previous newline (or start of body)
		lineStart := bytes.LastIndexByte(body[:pos], '\n')
		if lineStart == -1 {
			lineStart = 0
		} else {
			lineStart++ // Move past the newline
		}

		line := bytes.TrimSpace(body[lineStart:pos])
		pos = lineStart - 1 // Move position before the newline for next iteration

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
			return parseMetrics(modelID, start, gjson.ParseBytes(data))
		}
	}

	return TokenMetrics{}, fmt.Errorf("no valid JSON data found in stream")
}

func parseMetrics(modelID string, start time.Time, jsonData gjson.Result) (TokenMetrics, error) {
	usage := jsonData.Get("usage")
	timings := jsonData.Get("timings")
	if !usage.Exists() && !timings.Exists() {
		return TokenMetrics{}, fmt.Errorf("no usage or timings data found")
	}
	// default values
	cachedTokens := -1 // unknown or missing data
	outputTokens := 0
	inputTokens := 0

	// timings data
	tokensPerSecond := -1.0
	promptPerSecond := -1.0
	durationMs := int(time.Since(start).Milliseconds())

	if usage.Exists() {
		outputTokens = int(jsonData.Get("usage.completion_tokens").Int())
		inputTokens = int(jsonData.Get("usage.prompt_tokens").Int())
	}

	// use llama-server's timing data for tok/sec and duration as it is more accurate
	if timings.Exists() {
		inputTokens = int(jsonData.Get("timings.prompt_n").Int())
		outputTokens = int(jsonData.Get("timings.predicted_n").Int())
		promptPerSecond = jsonData.Get("timings.prompt_per_second").Float()
		tokensPerSecond = jsonData.Get("timings.predicted_per_second").Float()
		durationMs = int(jsonData.Get("timings.prompt_ms").Float() + jsonData.Get("timings.predicted_ms").Float())

		if cachedValue := jsonData.Get("timings.cache_n"); cachedValue.Exists() {
			cachedTokens = int(cachedValue.Int())
		}
	}

	return TokenMetrics{
		Timestamp:       time.Now(),
		Model:           modelID,
		CachedTokens:    cachedTokens,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		PromptPerSecond: promptPerSecond,
		TokensPerSecond: tokensPerSecond,
		DurationMs:      durationMs,
	}, nil
}

// ResponseBodyCopier records the response body and writes to the original response writer
// while also capturing it in a buffer for later processing
type ResponseBodyCopier struct {
	gin.ResponseWriter
	body  *bytes.Buffer
	tee   io.Writer
	start time.Time
}

func NewMetricsBodyRecorder(w gin.ResponseWriter) *ResponseBodyCopier {
	bodyBuffer := &bytes.Buffer{}
	return &ResponseBodyCopier{
		ResponseWriter: w,
		body:           bodyBuffer,
		tee:            io.MultiWriter(w, bodyBuffer),
	}
}

func (w *ResponseBodyCopier) Write(b []byte) (int, error) {
	if w.start.IsZero() {
		w.start = time.Now()
	}

	// Single write operation that writes to both the response and buffer
	return w.tee.Write(b)
}

func (w *ResponseBodyCopier) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *ResponseBodyCopier) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *ResponseBodyCopier) StartTime() time.Time {
	return w.start
}
