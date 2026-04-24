package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/klauspost/compress/zstd"
	"github.com/mostlygeek/llama-swap/event"
	"github.com/tidwall/gjson"
)

// zstdEncOptions are the shared zstd encoder options for maximum compression.
var zstdEncOptions = []zstd.EOption{
	zstd.WithEncoderLevel(zstd.SpeedBetterCompression),
}

// zstdDecOptions are the shared zstd decoder options.
var zstdDecOptions = []zstd.DOption{}

// zstdEncPool pools zstd.Encoder instances to reduce allocations.
var zstdEncPool = &sync.Pool{
	New: func() interface{} {
		enc, _ := zstd.NewWriter(nil, zstdEncOptions...)
		return enc
	},
}

// zstdDecPool pools zstd.Decoder instances to reduce allocations.
var zstdDecPool = &sync.Pool{
	New: func() interface{} {
		dec, _ := zstd.NewReader(nil, zstdDecOptions...)
		return dec
	},
}

// compressCapture marshals a ReqRespCapture to JSON and compresses it with zstd.
// Returns compressed bytes and the original JSON byte count for logging.
func compressCapture(c *ReqRespCapture) ([]byte, int, error) {
	jsonBytes, err := json.Marshal(c)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal capture: %w", err)
	}
	enc := zstdEncPool.Get().(*zstd.Encoder)
	defer zstdEncPool.Put(enc)
	return enc.EncodeAll(jsonBytes, nil), len(jsonBytes), nil
}

// decompressCapture decompresses zstd-compressed JSON and returns it.
func decompressCapture(data []byte) ([]byte, error) {
	dec := zstdDecPool.Get().(*zstd.Decoder)
	defer zstdDecPool.Put(dec)
	return dec.DecodeAll(data, nil)
}

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
	HasCapture      bool      `json:"has_capture"`
}

type ReqRespCapture struct {
	ID          int               `json:"id"`
	ReqPath     string            `json:"req_path"`
	ReqHeaders  map[string]string `json:"req_headers"`
	ReqBody     []byte            `json:"req_body"`
	RespHeaders map[string]string `json:"resp_headers"`
	RespBody    []byte            `json:"resp_body"`
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
	config   config.Config
	mu         sync.RWMutex
	metrics    []TokenMetrics
	maxMetrics int
	nextID     int
	logger     *LogMonitor

	// capture fields
	enableCaptures bool
	captures       map[int][]byte // zstd-compressed JSON of ReqRespCapture
	captureOrder   []int          // track insertion order for FIFO eviction
	captureSize    int            // current total compressed size in bytes
	maxCaptureSize int            // max bytes for captures (uncompressed)
}

// newMetricsMonitor creates a new metricsMonitor. captureBufferMB is the
// capture buffer size in megabytes; 0 disables captures.
func newMetricsMonitor(cfg config.Config, logger *LogMonitor, maxMetrics int, captureBufferMB int) *metricsMonitor {
	return &metricsMonitor{
		config:     cfg,
		logger:         logger,
		maxMetrics:     maxMetrics,
		enableCaptures: captureBufferMB > 0,
		captures:       make(map[int][]byte),
		captureOrder:   make([]int, 0),
		captureSize:    0,
		maxCaptureSize: captureBufferMB * 1024 * 1024,
	}
}

// addMetrics adds a new metric to the collection and publishes an event.
// Returns the assigned metric ID.
func (mp *metricsMonitor) addMetrics(metric TokenMetrics) int {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	metric.ID = mp.nextID
	// Resolve modelID to display name (first alias or modelID itself)
	if modelConfig, exists := mp.config.Models[metric.Model]; exists && len(modelConfig.Aliases) > 0 {
		metric.Model = modelConfig.Aliases[0]
	}
	mp.nextID++
	mp.metrics = append(mp.metrics, metric)
	if len(mp.metrics) > mp.maxMetrics {
		mp.metrics = mp.metrics[len(mp.metrics)-mp.maxMetrics:]
	}
	event.Emit(TokenMetricsEvent{Metrics: metric})
	return metric.ID
}

// addCapture adds a new capture to the buffer with size-based eviction.
// Captures are skipped if enableCaptures is false or if compressed data exceeds maxCaptureSize.
func (mp *metricsMonitor) addCapture(capture ReqRespCapture) {
	if !mp.enableCaptures {
		return
	}

	compressed, uncompressedBytes, err := compressCapture(&capture)
	if err != nil {
		mp.logger.Warnf("failed to compress capture: %v, skipping", err)
		return
	}

	captureSize := len(compressed)
	if captureSize > mp.maxCaptureSize {
		mp.logger.Warnf("compressed capture size %d exceeds max %d, skipping", captureSize, mp.maxCaptureSize)
		return
	}

	compressionRatio := (1 - float64(captureSize)/float64(uncompressedBytes)) * 100

	mp.mu.Lock()
	defer mp.mu.Unlock()

	// Evict oldest (FIFO) until room available for the compressed data
	for mp.captureSize+captureSize > mp.maxCaptureSize && len(mp.captureOrder) > 0 {
		oldestID := mp.captureOrder[0]
		mp.captureOrder = mp.captureOrder[1:]
		if evicted, exists := mp.captures[oldestID]; exists {
			l := len(evicted)
			mp.captureSize -= l
			delete(mp.captures, oldestID)
			mp.logger.Debugf("Capture %d evicted to make space: %d bytes", oldestID, l)
		}
	}

	mp.captures[capture.ID] = compressed
	mp.captureOrder = append(mp.captureOrder, capture.ID)
	mp.captureSize += captureSize

	mp.logger.Debugf("Capture %d compressed and saved: %d bytes -> %d bytes (%.1f%% compression)", capture.ID, uncompressedBytes, len(compressed), compressionRatio)
}

// getCompressedBytes returns the raw compressed bytes for a capture by ID.
func (mp *metricsMonitor) getCompressedBytes(id int) ([]byte, bool) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	data, exists := mp.captures[id]
	return data, exists
}

// getCaptureByID returns decompressed capture bytes if found and decompress=true.
// If decompress=false, returns the raw zstd-compressed bytes.
// Returns nil if the capture is not found.
func (mp *metricsMonitor) getCaptureByID(id int, decompress bool) []byte {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	data, exists := mp.captures[id]
	if !exists {
		return nil
	}

	if !decompress {
		return data
	}

	decompressed, err := decompressCapture(data)
	if err != nil {
		mp.logger.Warnf("failed to decompress capture %d: %v", id, err)
		return nil
	}

	return decompressed
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

// wrapHandler wraps the proxy handler to extract token metrics
// if wrapHandler returns an error it is safe to assume that no
// data was sent to the client
func (mp *metricsMonitor) wrapHandler(
	modelID string,
	writer gin.ResponseWriter,
	request *http.Request,
	next func(modelID string, w http.ResponseWriter, r *http.Request) error,
) error {
	// Capture request body and headers if captures enabled
	var reqBody []byte
	var reqHeaders map[string]string
	if mp.enableCaptures {
		if request.Body != nil {
			var err error
			reqBody, err = io.ReadAll(request.Body)
			if err != nil {
				return fmt.Errorf("failed to read request body for capture: %w", err)
			}
			request.Body.Close()
			request.Body = io.NopCloser(bytes.NewBuffer(reqBody))
		}
		reqHeaders = make(map[string]string)
		for key, values := range request.Header {
			if len(values) > 0 {
				reqHeaders[key] = values[0]
			}
		}
		redactHeaders(reqHeaders)
	}

	recorder := newBodyCopier(writer)

	// Filter Accept-Encoding to only include encodings we can decompress for metrics
	if ae := request.Header.Get("Accept-Encoding"); ae != "" {
		request.Header.Set("Accept-Encoding", filterAcceptEncoding(ae))
	}

	// Capture wall clock time before proxying the request
	requestStart := time.Now()

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
		DurationMs: int(time.Since(requestStart).Milliseconds()),
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
		if parsed, err := processStreamingResponse(modelID, requestStart, body); err != nil {
			mp.logger.Warnf("error processing streaming response: %v, path=%s, recording minimal metrics", err, request.URL.Path)
		} else {
			tm = parsed
		}
	} else {
		if gjson.ValidBytes(body) {
			parsed := gjson.ParseBytes(body)
			usage := parsed.Get("usage")
			timings := parsed.Get("timings")

			// extract timings for infill - response is an array, timings are in the last element
			// see #463
			if strings.HasPrefix(request.URL.Path, "/infill") {
				if arr := parsed.Array(); len(arr) > 0 {
					timings = arr[len(arr)-1].Get("timings")
				}
			}

			if usage.Exists() || timings.Exists() {
				if parsedMetrics, err := parseMetrics(modelID, requestStart, usage, timings); err != nil {
					mp.logger.Warnf("error parsing metrics: %v, path=%s, recording minimal metrics", err, request.URL.Path)
				} else {
					tm = parsedMetrics
				}
			}
		} else {
			mp.logger.Warnf("metrics: invalid JSON in response body path=%s, recording minimal metrics", request.URL.Path)
		}
	}

	// Build capture if enabled and determine if it will be stored
	var capture *ReqRespCapture
	if mp.enableCaptures {
		respHeaders := make(map[string]string)
		for key, values := range recorder.Header() {
			if len(values) > 0 {
				respHeaders[key] = values[0]
			}
		}
		redactHeaders(respHeaders)
		delete(respHeaders, "Content-Encoding")
		capture = &ReqRespCapture{
			ReqPath:     request.URL.Path,
			ReqHeaders:  reqHeaders,
			ReqBody:     reqBody,
			RespHeaders: respHeaders,
			RespBody:    body,
		}
		compressed, _, err := compressCapture(capture)
		if err == nil && len(compressed) <= mp.maxCaptureSize {
			tm.HasCapture = true
		}
	}

	metricID := mp.addMetrics(tm)

	// Store capture if enabled
	if capture != nil {
		capture.ID = metricID
		mp.addCapture(*capture)
	}

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

			// v1/responses format nests usage under response.usage
			if !usage.Exists() {
				usage = parsed.Get("response.usage")
			}

			if usage.Exists() || timings.Exists() {
				return parseMetrics(modelID, start, usage, timings)
			}
		}
	}

	return TokenMetrics{}, fmt.Errorf("no valid JSON data found in stream")
}

func parseMetrics(modelID string, start time.Time, usage, timings gjson.Result) (TokenMetrics, error) {
	wallDurationMs := int(time.Since(start).Milliseconds())

	// default values
	cachedTokens := -1 // unknown or missing data
	outputTokens := 0
	inputTokens := 0

	// timings data
	tokensPerSecond := -1.0
	promptPerSecond := -1.0
	durationMs := wallDurationMs

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
		timingsDurationMs := int(timings.Get("prompt_ms").Float() + timings.Get("predicted_ms").Float())
		if timingsDurationMs > durationMs {
			durationMs = timingsDurationMs
		}

		if cachedValue := timings.Get("cache_n"); cachedValue.Exists() {
			cachedTokens = int(cachedValue.Int())
		}
	}

	// Fallback: estimate speeds from wall clock when timings unavailable (e.g., vLLM)
	if !timings.Exists() && wallDurationMs > 0 {
		durationSec := float64(wallDurationMs) / 1000.0
		if inputTokens > 0 {
			promptPerSecond = float64(inputTokens) / durationSec
		}
		if outputTokens > 0 {
			tokensPerSecond = float64(outputTokens) / durationSec
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

// sensitiveHeaders lists headers that should be redacted in captures
var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
}

// redactHeaders replaces sensitive header values in-place with "[REDACTED]"
func redactHeaders(headers map[string]string) {
	for key := range headers {
		if sensitiveHeaders[strings.ToLower(key)] {
			headers[key] = "[REDACTED]"
		}
	}
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

	for part := range strings.SplitSeq(acceptEncoding, ",") {
		// Parse encoding and optional quality value (e.g., "gzip;q=1.0")
		encoding, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if supported[strings.ToLower(encoding)] {
			filtered = append(filtered, strings.TrimSpace(part))
		}
	}

	return strings.Join(filtered, ", ")
}
