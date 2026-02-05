package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
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

// metricsMonitor parses llama-server output for token statistics
type metricsMonitor struct {
	mu            sync.RWMutex
	metrics       []TokenMetrics
	maxMetrics    int
	nextID        int
	logger        *LogMonitor
	rollup        metricsRollup
	rollupByModel map[string]*metricsRollup
}

// metricsRollup holds aggregated metrics data from prior requests
type metricsRollup struct {
	RequestsTotal     uint64
	InputTokensTotal  uint64
	OutputTokensTotal uint64
	CachedTokensTotal uint64
}

func newMetricsMonitor(logger *LogMonitor, maxMetrics int) *metricsMonitor {
	mp := &metricsMonitor{
		logger:        logger,
		maxMetrics:    maxMetrics,
		rollupByModel: make(map[string]*metricsRollup),
	}

	return mp
}

// addMetrics adds a new metric to the collection and publishes an event
func (mp *metricsMonitor) addMetrics(metric TokenMetrics) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	metric.ID = mp.nextID
	mp.nextID++
	mp.metrics = append(mp.metrics, metric)
	if len(mp.metrics) > mp.maxMetrics {
		mp.metrics = mp.metrics[len(mp.metrics)-mp.maxMetrics:]
	}
	mp.updateTokenRollupCounters(metric)
	event.Emit(TokenMetricsEvent{Metrics: metric})
}

// update token counters at model level and global level
func (mp *metricsMonitor) updateTokenRollupCounters(metric TokenMetrics) {
	updateRollup(&mp.rollup, metric)

	modelRollup, ok := mp.rollupByModel[metric.Model]
	if !ok {
		modelRollup = &metricsRollup{}
		mp.rollupByModel[metric.Model] = modelRollup
	}
	updateRollup(modelRollup, metric)
}

// updateRollup increases counters based on the given metric object
func updateRollup(rollup *metricsRollup, metric TokenMetrics) {
	rollup.RequestsTotal++
	if metric.InputTokens >= 0 {
		rollup.InputTokensTotal += uint64(metric.InputTokens)
	}
	if metric.OutputTokens >= 0 {
		rollup.OutputTokensTotal += uint64(metric.OutputTokens)
	}
	if metric.CachedTokens >= 0 {
		rollup.CachedTokensTotal += uint64(metric.CachedTokens)
	}
}

// getMetrics returns a copy of the current metrics
func (mp *metricsMonitor) getMetrics() []TokenMetrics {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	result := make([]TokenMetrics, len(mp.metrics))
	copy(result, mp.metrics)
	return result
}

// getMetricsJSON returns metrics as JSON
func (mp *metricsMonitor) getMetricsJSON() ([]byte, error) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return json.Marshal(mp.metrics)
}

// getPrometheusText returns the metrics for the current monitor in Prometheus text format
func (mp *metricsMonitor) getPrometheusText() []byte {
	mp.mu.RLock()
	overall := mp.rollup
	perModel := make(map[string]metricsRollup, len(mp.rollupByModel))
	for model, rollup := range mp.rollupByModel {
		if rollup != nil {
			perModel[model] = *rollup
		}
	}
	metricsCopy := make([]TokenMetrics, len(mp.metrics))
	copy(metricsCopy, mp.metrics)
	mp.mu.RUnlock()

	models := make([]string, 0, len(perModel))
	for model := range perModel {
		models = append(models, model)
	}
	sort.Strings(models)

	var b strings.Builder
	writeCounterWithModel(&b, "llama_swap_requests_total", "Total number of requests with recorded metrics.", overall.RequestsTotal, models, perModel, func(r metricsRollup) uint64 {
		return r.RequestsTotal
	})
	writeCounterWithModel(&b, "llama_swap_input_tokens_total", "Total input tokens recorded.", overall.InputTokensTotal, models, perModel, func(r metricsRollup) uint64 {
		return r.InputTokensTotal
	})
	writeCounterWithModel(&b, "llama_swap_output_tokens_total", "Total output tokens recorded.", overall.OutputTokensTotal, models, perModel, func(r metricsRollup) uint64 {
		return r.OutputTokensTotal
	})
	writeCounterWithModel(&b, "llama_swap_cached_tokens_total", "Total cached tokens recorded.", overall.CachedTokensTotal, models, perModel, func(r metricsRollup) uint64 {
		return r.CachedTokensTotal
	})

	windowSizes := []int{1, 5, 15}
	overallGen, overallPrompt, perModelGen, perModelPrompt := computeTokensPerSecondLastN(metricsCopy, windowSizes)
	for _, windowSize := range windowSizes {
		writeGaugeWithModel(&b, fmt.Sprintf("llama_swap_generate_tokens_per_second_last_%d", windowSize), fmt.Sprintf("Average generation tokens per second over last %d requests.", windowSize), overallGen[windowSize], models, func(model string) float64 {
			if modelValues, ok := perModelGen[model]; ok {
				return modelValues[windowSize]
			}
			return 0
		})
		writeGaugeWithModel(&b, fmt.Sprintf("llama_swap_prompt_tokens_per_second_last_%d", windowSize), fmt.Sprintf("Average prompt tokens per second over last %d requests.", windowSize), overallPrompt[windowSize], models, func(model string) float64 {
			if modelValues, ok := perModelPrompt[model]; ok {
				return modelValues[windowSize]
			}
			return 0
		})
	}

	return []byte(b.String())
}

// writeCounterWithModel writes a Prometheus counter metric with per-model breakdown
func writeCounterWithModel(
	b *strings.Builder,
	name string,
	help string,
	overall uint64,
	models []string,
	perModel map[string]metricsRollup,
	getValue func(metricsRollup) uint64,
) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s counter\n", name)
	fmt.Fprintf(b, "%s %d\n", name, overall)
	for _, model := range models {
		value := getValue(perModel[model])
		fmt.Fprintf(b, "%s{model=\"%s\"} %d\n", name, promLabelValue(model), value)
	}
}

// writeGaugeWithModel writes a Prometheus gauge metric with per-model breakdown
func writeGaugeWithModel(
	b *strings.Builder,
	name string,
	help string,
	overall float64,
	models []string,
	getValue func(model string) float64,
) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s gauge\n", name)
	fmt.Fprintf(b, "%s %s\n", name, formatFloat(overall))
	for _, model := range models {
		fmt.Fprintf(b, "%s{model=\"%s\"} %s\n", name, promLabelValue(model), formatFloat(getValue(model)))
	}
}

// computeTokensPerSecondLastN looks at a window size of the last N metrics and calculates the average tokens per second
func computeTokensPerSecondLastN(metrics []TokenMetrics, windowSizes []int) (map[int]float64, map[int]float64, map[string]map[int]float64, map[string]map[int]float64) {
	overallGenSum := make(map[int]float64)
	overallGenCount := make(map[int]int)
	overallPromptSum := make(map[int]float64)
	overallPromptCount := make(map[int]int)
	overallSeen := make(map[int]int)

	perModelSeen := make(map[string]map[int]int)
	perModelGenSum := make(map[string]map[int]float64)
	perModelGenCount := make(map[string]map[int]int)
	perModelPromptSum := make(map[string]map[int]float64)
	perModelPromptCount := make(map[string]map[int]int)

	// iterate over metrics in reverse order to get the most recent first
	for i := len(metrics) - 1; i >= 0; i-- {
		metric := metrics[i]
		model := metric.Model

		if _, ok := perModelSeen[model]; !ok {
			perModelSeen[model] = make(map[int]int)
			perModelGenSum[model] = make(map[int]float64)
			perModelGenCount[model] = make(map[int]int)
			perModelPromptSum[model] = make(map[int]float64)
			perModelPromptCount[model] = make(map[int]int)
		}

		// calculate for each window size at the same time
		for _, windowSize := range windowSizes {
			if overallSeen[windowSize] < windowSize {
				overallSeen[windowSize]++
				if metric.TokensPerSecond >= 0 {
					overallGenSum[windowSize] += metric.TokensPerSecond
					overallGenCount[windowSize]++
				}
				if metric.PromptPerSecond >= 0 {
					overallPromptSum[windowSize] += metric.PromptPerSecond
					overallPromptCount[windowSize]++
				}
			}

			if perModelSeen[model][windowSize] < windowSize {
				perModelSeen[model][windowSize]++
				if metric.TokensPerSecond >= 0 {
					perModelGenSum[model][windowSize] += metric.TokensPerSecond
					perModelGenCount[model][windowSize]++
				}
				if metric.PromptPerSecond >= 0 {
					perModelPromptSum[model][windowSize] += metric.PromptPerSecond
					perModelPromptCount[model][windowSize]++
				}
			}
		}
	}

	// calculate averages for each window size
	overallGen := make(map[int]float64)
	overallPrompt := make(map[int]float64)
	perModelGen := make(map[string]map[int]float64)
	perModelPrompt := make(map[string]map[int]float64)

	for _, windowSize := range windowSizes {
		if overallGenCount[windowSize] > 0 {
			overallGen[windowSize] = overallGenSum[windowSize] / float64(overallGenCount[windowSize])
		} else {
			overallGen[windowSize] = 0
		}
		if overallPromptCount[windowSize] > 0 {
			overallPrompt[windowSize] = overallPromptSum[windowSize] / float64(overallPromptCount[windowSize])
		} else {
			overallPrompt[windowSize] = 0
		}
	}

	for model := range perModelSeen {
		perModelGen[model] = make(map[int]float64)
		perModelPrompt[model] = make(map[int]float64)
		for _, windowSize := range windowSizes {
			if perModelGenCount[model][windowSize] > 0 {
				perModelGen[model][windowSize] = perModelGenSum[model][windowSize] / float64(perModelGenCount[model][windowSize])
			} else {
				perModelGen[model][windowSize] = 0
			}
			if perModelPromptCount[model][windowSize] > 0 {
				perModelPrompt[model][windowSize] = perModelPromptSum[model][windowSize] / float64(perModelPromptCount[model][windowSize])
			} else {
				perModelPrompt[model][windowSize] = 0
			}
		}
	}

	return overallGen, overallPrompt, perModelGen, perModelPrompt
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func promLabelValue(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"")
	return replacer.Replace(value)
}

// wrapHandler wraps the proxy handler to extract token metrics
// if wrapHandler returns an error it is safe to assume that no
// data was sent to the client
func (mp *metricsMonitor) wrapHandler(
	modelID string,
	writer gin.ResponseWriter,
	request *http.Request,
	next func(modelID string, w http.ResponseWriter, r *http.Request) error,
) error {
	recorder := newBodyCopier(writer)

	// Filter Accept-Encoding to only include encodings we can decompress for metrics
	if ae := request.Header.Get("Accept-Encoding"); ae != "" {
		request.Header.Set("Accept-Encoding", filterAcceptEncoding(ae))
	}

	if err := next(modelID, recorder, request); err != nil {
		return err
	}

	// after this point we have to assume that data was sent to the client
	// and we can only log errors but not send them to clients

	if recorder.Status() != http.StatusOK {
		mp.logger.Warnf("metrics skipped, HTTP status=%d, path=%s", recorder.Status(), request.URL.Path)
		return nil
	}

	// Initialize default metrics - these will always be recorded
	tm := TokenMetrics{
		Timestamp:  time.Now(),
		Model:      modelID,
		DurationMs: int(time.Since(recorder.StartTime()).Milliseconds()),
	}

	body := recorder.body.Bytes()
	if len(body) == 0 {
		mp.logger.Warn("metrics: empty body, recording minimal metrics")
		mp.addMetrics(tm)
		return nil
	}

	// Decompress if needed
	if encoding := recorder.Header().Get("Content-Encoding"); encoding != "" {
		var err error
		body, err = decompressBody(body, encoding)
		if err != nil {
			mp.logger.Warnf("metrics: decompression failed: %v, path=%s, recording minimal metrics", err, request.URL.Path)
			mp.addMetrics(tm)
			return nil
		}
	}

	if strings.Contains(recorder.Header().Get("Content-Type"), "text/event-stream") {
		if parsed, err := processStreamingResponse(modelID, recorder.StartTime(), body); err != nil {
			mp.logger.Warnf("error processing streaming response: %v, path=%s, recording minimal metrics", err, request.URL.Path)
		} else {
			tm = parsed
		}
	} else {
		if gjson.ValidBytes(body) {
			parsed := gjson.ParseBytes(body)
			usage := parsed.Get("usage")
			timings := parsed.Get("timings")

			if usage.Exists() || timings.Exists() {
				if parsedMetrics, err := parseMetrics(modelID, recorder.StartTime(), usage, timings); err != nil {
					mp.logger.Warnf("error parsing metrics: %v, path=%s, recording minimal metrics", err, request.URL.Path)
				} else {
					tm = parsedMetrics
				}
			}
		} else {
			mp.logger.Warnf("metrics: invalid JSON in response body path=%s, recording minimal metrics", request.URL.Path)
		}
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
			parsed := gjson.ParseBytes(data)
			usage := parsed.Get("usage")
			timings := parsed.Get("timings")

			if usage.Exists() || timings.Exists() {
				return parseMetrics(modelID, start, usage, timings)
			}
		}
	}

	return TokenMetrics{}, fmt.Errorf("no valid JSON data found in stream")
}

func parseMetrics(modelID string, start time.Time, usage, timings gjson.Result) (TokenMetrics, error) {
	// default values
	cachedTokens := -1 // unknown or missing data
	outputTokens := 0
	inputTokens := 0

	// timings data
	tokensPerSecond := -1.0
	promptPerSecond := -1.0
	durationMs := int(time.Since(start).Milliseconds())

	if usage.Exists() {
		if pt := usage.Get("prompt_tokens"); pt.Exists() {
			// v1/chat/completions
			inputTokens = int(pt.Int())
		} else if it := usage.Get("input_tokens"); it.Exists() {
			// v1/messages
			inputTokens = int(it.Int())
		}

		if ct := usage.Get("completion_tokens"); ct.Exists() {
			// v1/chat/completions
			outputTokens = int(ct.Int())
		} else if ot := usage.Get("output_tokens"); ot.Exists() {
			outputTokens = int(ot.Int())
		}

		if ct := usage.Get("cache_read_input_tokens"); ct.Exists() {
			cachedTokens = int(ct.Int())
		}
	}

	// use llama-server's timing data for tok/sec and duration as it is more accurate
	if timings.Exists() {
		inputTokens = int(timings.Get("prompt_n").Int())
		outputTokens = int(timings.Get("predicted_n").Int())
		promptPerSecond = timings.Get("prompt_per_second").Float()
		tokensPerSecond = timings.Get("predicted_per_second").Float()
		durationMs = int(timings.Get("prompt_ms").Float() + timings.Get("predicted_ms").Float())

		if cachedValue := timings.Get("cache_n"); cachedValue.Exists() {
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

// decompressBody decompresses the body based on Content-Encoding header
func decompressBody(body []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	case "deflate":
		reader := flate.NewReader(bytes.NewReader(body))
		defer reader.Close()
		return io.ReadAll(reader)
	default:
		return body, nil // Return as-is for unknown/no encoding
	}
}

// responseBodyCopier records the response body and writes to the original response writer
// while also capturing it in a buffer for later processing
type responseBodyCopier struct {
	gin.ResponseWriter
	body  *bytes.Buffer
	tee   io.Writer
	start time.Time
}

func newBodyCopier(w gin.ResponseWriter) *responseBodyCopier {
	bodyBuffer := &bytes.Buffer{}
	return &responseBodyCopier{
		ResponseWriter: w,
		body:           bodyBuffer,
		tee:            io.MultiWriter(w, bodyBuffer),
	}
}

func (w *responseBodyCopier) Write(b []byte) (int, error) {
	if w.start.IsZero() {
		w.start = time.Now()
	}

	// Single write operation that writes to both the response and buffer
	return w.tee.Write(b)
}

func (w *responseBodyCopier) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseBodyCopier) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *responseBodyCopier) StartTime() time.Time {
	return w.start
}

// filterAcceptEncoding filters the Accept-Encoding header to only include
// encodings we can decompress (gzip, deflate). This respects the client's
// preferences while ensuring we can parse response bodies for metrics.
func filterAcceptEncoding(acceptEncoding string) string {
	if acceptEncoding == "" {
		return ""
	}

	supported := map[string]bool{"gzip": true, "deflate": true}
	var filtered []string

	for _, part := range strings.Split(acceptEncoding, ",") {
		// Parse encoding and optional quality value (e.g., "gzip;q=1.0")
		encoding := strings.TrimSpace(strings.Split(part, ";")[0])
		if supported[strings.ToLower(encoding)] {
			filtered = append(filtered, strings.TrimSpace(part))
		}
	}

	return strings.Join(filtered, ", ")
}
