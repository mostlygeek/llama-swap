package server

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

	"github.com/mostlygeek/llama-swap/internal/cache"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/ring"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/tidwall/gjson"
)

// TokenMetrics holds token usage and performance metrics.
type TokenMetrics struct {
	CachedTokens    int     `json:"cache_tokens"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	PromptPerSecond float64 `json:"prompt_per_second"`
	TokensPerSecond float64 `json:"tokens_per_second"`
}

// ActivityLogEntry represents parsed token statistics from llama-server logs.
type ActivityLogEntry struct {
	ID              int          `json:"id"`
	Timestamp       time.Time    `json:"timestamp"`
	Model           string       `json:"model"`
	ReqPath         string       `json:"req_path"`
	RespContentType string       `json:"resp_content_type"`
	RespStatusCode  int          `json:"resp_status_code"`
	Tokens          TokenMetrics `json:"tokens"`
	DurationMs      int          `json:"duration_ms"`
	HasCapture      bool         `json:"has_capture"`
}

// ActivityLogEvent carries a single activity log entry to event subscribers.
type ActivityLogEvent struct {
	Metrics ActivityLogEntry
}

func (e ActivityLogEvent) Type() uint32 {
	return shared.ActivityLogEventID
}

// metricsMonitor parses upstream responses for token statistics, keeps a
// bounded in-memory ring of recent activity, and (when captures are enabled)
// stores zstd+CBOR-compressed request/response captures in a sized cache.
// When a MetricsFileStore is configured, metrics survive server restarts.
type metricsMonitor struct {
	mu      sync.RWMutex
	metrics ring.Buffer[ActivityLogEntry]
	nextID  int
	logger  *logmon.Monitor

	enableCaptures bool
	captureCache   *cache.Cache // zstd-compressed CBOR of ReqRespCapture

	store *MetricsFileStore
}

// newMetricsMonitor creates a metricsMonitor retaining up to maxMetrics entries.
// captureBufferMB is the capture buffer size in megabytes; 0 disables captures.
// When storeFile is non-empty, metrics are persisted to that path (JSON format)
// with periodic flushes at storeInterval (0 = 5s default).
func newMetricsMonitor(logger *logmon.Monitor, maxMetrics int, captureBufferMB int, storeFile string, storeInterval time.Duration) *metricsMonitor {
	if maxMetrics <= 0 {
		maxMetrics = 1000
	}
	mm := &metricsMonitor{
		logger:         logger,
		metrics:        ring.NewBuffer[ActivityLogEntry](maxMetrics),
		enableCaptures: captureBufferMB > 0,
	}
	if captureBufferMB > 0 {
		mm.captureCache = cache.New(captureBufferMB * 1024 * 1024)
	}

	// File-backed persistence for metrics
	if storeFile != "" {
		mm.store = NewMetricsFileStore(storeFile, storeInterval, mm.getMetrics)
		if mm.store != nil {
			if existing, err := mm.store.Load(); err != nil {
				logger.Warnf("metrics: failed to load stored metrics: %v", err)
			} else if len(existing) > 0 {
				for i, e := range existing {
					if e.ID >= mm.nextID {
						mm.nextID = e.ID + 1
					}
					mm.metrics.Push(e)
					_ = i
				}
				logger.Infof("metrics: loaded %d entries from %s", len(existing), storeFile)
			}
			mm.store.Start()
		}
	}

	return mm
}

// queueMetrics adds a metric to the ring and returns its assigned ID.
func (mp *metricsMonitor) queueMetrics(metric ActivityLogEntry) int {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	metric.ID = mp.nextID
	mp.nextID++
	mp.metrics.Push(metric)
	return metric.ID
}

// emitMetric publishes an ActivityLogEvent for the given metric.
func (mp *metricsMonitor) emitMetric(metric ActivityLogEntry) {
	event.Emit(ActivityLogEvent{Metrics: metric})
}

// getMetrics returns a copy of the current metrics.
func (mp *metricsMonitor) getMetrics() []ActivityLogEntry {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	result := mp.metrics.Slice()
	if result == nil {
		return []ActivityLogEntry{}
	}
	if mp.captureCache != nil {
		for i := range result {
			result[i].HasCapture = mp.captureCache.Has(result[i].ID)
		}
	}
	return result
}

// getMetricsJSON returns the current metrics as a JSON array.
func (mp *metricsMonitor) getMetricsJSON() ([]byte, error) {
	return json.Marshal(mp.getMetrics())
}

// record parses a completed response body and stores/emits an activity entry.
// When captures are enabled, a zstd+CBOR capture is stored for successful
// requests, with cf controlling which request/response parts are retained.
// reqBody and reqHeaders are the request data buffered before dispatch.
func (mp *metricsMonitor) record(modelID string, r *http.Request, recorder *responseBodyCopier, cf captureFields, reqBody []byte, reqHeaders map[string]string) {
	// Rate and cache fields default to -1 (the "unknown" sentinel rendered as
	// "unknown" in the UI) so failure paths — and vLLM streaming requests that
	// omit usage chunks — don't surface 0 t/s as if it were a real measurement.
	// Token counts stay at 0. Successful parses overwrite tm.Tokens below.
	tm := ActivityLogEntry{
		Timestamp:       time.Now(),
		Model:           modelID,
		ReqPath:         r.URL.Path,
		RespContentType: recorder.Header().Get("Content-Type"),
		RespStatusCode:  recorder.Status(),
		DurationMs:      int(time.Since(recorder.StartTime()).Milliseconds()),
		Tokens: TokenMetrics{
			CachedTokens:    -1,
			PromptPerSecond: -1,
			TokensPerSecond: -1,
		},
	}

	queueAndEmit := func() {
		tm.ID = mp.queueMetrics(tm)
		mp.emitMetric(tm)
	}

	if recorder.Status() != http.StatusOK {
		mp.logger.Warnf("non-200 response, recording partial metrics: status=%d, path=%s", recorder.Status(), r.URL.Path)
		queueAndEmit()
		return
	}

	body := recorder.body.Bytes()
	if len(body) == 0 {
		mp.logger.Warn("metrics: empty body, recording minimal metrics")
		queueAndEmit()
		return
	}

	if encoding := recorder.Header().Get("Content-Encoding"); encoding != "" {
		decoded, err := decompressBody(body, encoding)
		if err != nil {
			mp.logger.Warnf("metrics: decompression failed: %v, path=%s, recording minimal metrics", err, r.URL.Path)
			queueAndEmit()
			return
		}
		body = decoded
	}

	if strings.Contains(recorder.Header().Get("Content-Type"), "text/event-stream") {
		if parsed, err := processStreamingResponse(modelID, recorder.StartTime(), recorder.FirstWriteTime(), body); err != nil {
			mp.logger.Warnf("error processing streaming response: %v, path=%s, recording minimal metrics", err, r.URL.Path)
		} else {
			tm.Tokens = parsed.Tokens
			tm.DurationMs = parsed.DurationMs
		}
	} else if gjson.ValidBytes(body) {
		parsed := gjson.ParseBytes(body)
		usage := parsed.Get("usage")
		timings := parsed.Get("timings")
		inBodyMetrics := findInBodyMetrics(parsed)

		// /infill responses are arrays; timings live in the last element (#463).
		if strings.HasPrefix(r.URL.Path, "/infill") {
			if arr := parsed.Array(); len(arr) > 0 {
				timings = arr[len(arr)-1].Get("timings")
			}
		}

		if usage.Exists() || timings.Exists() || inBodyMetrics.Exists() {
			if parsedMetrics, err := parseMetrics(modelID, recorder.StartTime(), usage, timings, inBodyMetrics); err != nil {
				mp.logger.Warnf("error parsing metrics: %v, path=%s, recording minimal metrics", err, r.URL.Path)
			} else {
				tm.Tokens = parsedMetrics.Tokens
				tm.DurationMs = parsedMetrics.DurationMs
			}
		}
	} else {
		mp.logger.Warnf("metrics: invalid JSON in response body path=%s, recording minimal metrics", r.URL.Path)
	}

	tm.ID = mp.queueMetrics(tm)
	if mp.enableCaptures {
		capture := ReqRespCapture{
			ID:         tm.ID,
			ReqPath:    r.URL.Path,
			ReqHeaders: reqHeaders,
		}
		if cf&captureReqBody != 0 {
			capture.ReqBody = reqBody
		}
		if cf&captureRespHeaders != 0 {
			capture.RespHeaders = headerMap(recorder.Header())
			redactHeaders(capture.RespHeaders)
			delete(capture.RespHeaders, "Content-Encoding")
		}
		if cf&captureRespBody != 0 {
			capture.RespBody = body
		}
		if mp.addCapture(capture) {
			tm.HasCapture = true
		}
	}
	mp.emitMetric(tm)
}

// usagePaths lists the JSON paths where a per-event usage object can live.
var usagePaths = []string{"usage", "response.usage", "message.usage"}

// inBodyMetricsPaths lists the JSON paths where an upstream may emit a
// per-request metrics object directly in the response body. Currently
// opportunistic: covers possible vLLM in-body metrics and any other upstream
// that chooses to emit one.
var inBodyMetricsPaths = []string{"metrics", "vllm_metrics"}

// findInBodyMetrics returns the first existing per-request metrics object at one
// of the known paths in the parsed JSON. Returns a zero gjson.Result when none
// is present.
func findInBodyMetrics(parsed gjson.Result) gjson.Result {
	for _, path := range inBodyMetricsPaths {
		if m := parsed.Get(path); m.Exists() {
			return m
		}
	}
	return gjson.Result{}
}

// extractUsageTokens reads input/output/cached token counts from a usage
// gjson.Result, handling the field-name differences across endpoints.
func extractUsageTokens(usage gjson.Result) (input, output, cached int64, ok bool) {
	cached = -1
	if !usage.Exists() {
		return
	}

	if v := usage.Get("prompt_tokens"); v.Exists() {
		input = v.Int()
		ok = true
	} else if v := usage.Get("input_tokens"); v.Exists() {
		input = v.Int()
		ok = true
	}

	if v := usage.Get("completion_tokens"); v.Exists() {
		output = v.Int()
		ok = true
	} else if v := usage.Get("output_tokens"); v.Exists() {
		output = v.Int()
		ok = true
	}

	if v := usage.Get("cache_read_input_tokens"); v.Exists() {
		cached = v.Int()
		ok = true
	} else if v := usage.Get("input_tokens_details.cached_tokens"); v.Exists() {
		cached = v.Int()
		ok = true
	} else if v := usage.Get("prompt_tokens_details.cached_tokens"); v.Exists() {
		cached = v.Int()
		ok = true
	}
	return
}

func processStreamingResponse(modelID string, start, firstWrite time.Time, body []byte) (ActivityLogEntry, error) {
	var (
		inputTokens, outputTokens int64
		cachedTokens              int64 = -1
		hasAny                    bool
		timings                   gjson.Result
		inBodyMetrics             gjson.Result
	)

	prefix := []byte("data:")
	for offset := 0; offset < len(body); {
		nl := bytes.IndexByte(body[offset:], '\n')
		var line []byte
		if nl == -1 {
			line = body[offset:]
			offset = len(body)
		} else {
			line = body[offset : offset+nl]
			offset += nl + 1
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.HasPrefix(line, prefix) {
			continue
		}
		data := bytes.TrimSpace(line[len(prefix):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		if !gjson.ValidBytes(data) {
			continue
		}
		parsed := gjson.ParseBytes(data)

		for _, path := range usagePaths {
			u := parsed.Get(path)
			if !u.Exists() {
				continue
			}
			i, o, c, ok := extractUsageTokens(u)
			if !ok {
				continue
			}
			hasAny = true
			if i > 0 {
				inputTokens = i
			}
			if o > 0 {
				outputTokens = o
			}
			if c >= 0 {
				cachedTokens = c
			}
		}
		if t := parsed.Get("timings"); t.Exists() {
			timings = t
			hasAny = true
		}
		if m := findInBodyMetrics(parsed); m.Exists() {
			inBodyMetrics = m
			hasAny = true
		}
	}

	if !hasAny {
		return ActivityLogEntry{}, fmt.Errorf("no valid JSON data found in stream")
	}

	return buildMetrics(modelID, start, firstWrite, inputTokens, outputTokens, cachedTokens, timings, inBodyMetrics), nil
}

func parseMetrics(modelID string, start time.Time, usage, timings, inBodyMetrics gjson.Result) (ActivityLogEntry, error) {
	input, output, cached, _ := extractUsageTokens(usage)
	// Non-streaming requests have no meaningful TTFT signal; pass a zero
	// firstWrite so buildMetrics falls through to the non-streaming tier.
	return buildMetrics(modelID, start, time.Time{}, input, output, cached, timings, inBodyMetrics), nil
}

// buildMetrics composes an ActivityLogEntry from accumulated token counts and
// derives per-request rates using a four-tier fallback (highest precedence
// first):
//  1. Upstream-emitted timings block (llama-server /v1/chat/completions), which
//     also overrides input/output token counts.
//  2. Opportunistic in-body metrics object (e.g., vLLM-style metrics).
//  3. Streaming TTFT split — when firstWrite is set, split the duration into
//     prefill (start..firstWrite) and decode (firstWrite..now) to derive rates.
//  4. Non-streaming approximation — fill tokens_per_second from output / duration
//     and leave prompt_per_second at -1 (no signal to separate prefill).
//
// Rates that cannot be derived honestly stay at -1 (the "unknown" sentinel);
// 0 and non-finite values are never emitted.
func buildMetrics(modelID string, start, firstWrite time.Time, inputTokens, outputTokens, cachedTokens int64, timings, inBodyMetrics gjson.Result) ActivityLogEntry {
	now := time.Now()
	wallDurationMs := int(now.Sub(start).Milliseconds())
	durationMs := wallDurationMs
	tokensPerSecond := -1.0
	promptPerSecond := -1.0

	switch {
	case timings.Exists():
		inputTokens = timings.Get("prompt_n").Int()
		outputTokens = timings.Get("predicted_n").Int()
		promptPerSecond = timings.Get("prompt_per_second").Float()
		tokensPerSecond = timings.Get("predicted_per_second").Float()
		timingsDurationMs := int(timings.Get("prompt_ms").Float() + timings.Get("predicted_ms").Float())
		if timingsDurationMs > durationMs {
			durationMs = timingsDurationMs
		}
		if cachedValue := timings.Get("cache_n"); cachedValue.Exists() {
			cachedTokens = cachedValue.Int()
		}

	case inBodyMetrics.Exists():
		if v := inBodyMetrics.Get("prompt_per_second"); v.Exists() && v.Float() > 0 {
			promptPerSecond = v.Float()
		}
		if v := inBodyMetrics.Get("tokens_per_second"); v.Exists() && v.Float() > 0 {
			tokensPerSecond = v.Float()
		}

	case !firstWrite.IsZero() && firstWrite.After(start) && !firstWrite.After(now):
		// Streaming TTFT split: prefill is the time to first byte, decode is the
		// remaining wall time. Guard each division on positive inputs so a
		// degenerate split leaves the rate at -1 rather than emitting 0/Inf.
		prefillMs := firstWrite.Sub(start).Milliseconds()
		decodeMs := int64(wallDurationMs) - prefillMs
		if prefillMs > 0 && inputTokens > 0 {
			promptPerSecond = float64(inputTokens) * 1000.0 / float64(prefillMs)
		}
		if decodeMs > 0 && outputTokens > 0 {
			tokensPerSecond = float64(outputTokens) * 1000.0 / float64(decodeMs)
		}

	default:
		// Non-streaming, no timings, no in-body metrics: only tokens_per_second
		// can be approximated; prompt_per_second stays -1 (no prefill signal).
		if wallDurationMs > 0 && outputTokens > 0 {
			tokensPerSecond = float64(outputTokens) * 1000.0 / float64(wallDurationMs)
		}
	}

	return ActivityLogEntry{
		Timestamp: now,
		Model:     modelID,
		Tokens: TokenMetrics{
			CachedTokens:    int(cachedTokens),
			InputTokens:     int(inputTokens),
			OutputTokens:    int(outputTokens),
			PromptPerSecond: promptPerSecond,
			TokensPerSecond: tokensPerSecond,
		},
		DurationMs: durationMs,
	}
}

// Close stops the background metrics persistence, flushing any pending data.
func (mp *metricsMonitor) Close() error {
	if mp.store != nil {
		return mp.store.Close()
	}
	return nil
}

// decompressBody decompresses the body based on the Content-Encoding header.
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
		return body, nil
	}
}

// filterAcceptEncoding filters Accept-Encoding to only gzip/deflate so response
// bodies remain decompressible for metrics parsing.
func filterAcceptEncoding(acceptEncoding string) string {
	if acceptEncoding == "" {
		return ""
	}

	supported := map[string]bool{"gzip": true, "deflate": true}
	var filtered []string
	for part := range strings.SplitSeq(acceptEncoding, ",") {
		encoding, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if supported[strings.ToLower(encoding)] {
			filtered = append(filtered, strings.TrimSpace(part))
		}
	}
	return strings.Join(filtered, ", ")
}

// responseBodyCopier tees the upstream response to the client while buffering
// it for metrics parsing. Status defaults to 200 until WriteHeader is called.
type responseBodyCopier struct {
	http.ResponseWriter
	body        *bytes.Buffer
	tee         io.Writer
	status      int
	wroteHeader bool
	start       time.Time
	firstWrite  time.Time
}

func newBodyCopier(w http.ResponseWriter) *responseBodyCopier {
	buf := &bytes.Buffer{}
	return &responseBodyCopier{
		ResponseWriter: w,
		body:           buf,
		tee:            io.MultiWriter(w, buf),
		status:         http.StatusOK,
		start:          time.Now(),
	}
}

func (w *responseBodyCopier) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.firstWrite.IsZero() && len(b) > 0 {
		w.firstWrite = time.Now()
	}
	return w.tee.Write(b)
}

func (w *responseBodyCopier) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Flush forwards to the underlying writer so streaming responses still flush.
func (w *responseBodyCopier) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseBodyCopier) Status() int          { return w.status }
func (w *responseBodyCopier) StartTime() time.Time { return w.start }

// FirstWriteTime returns the time at which the first non-empty byte was written
// to the response. Used to derive a proxy-side TTFT for streaming responses.
// Zero when no data has been written.
func (w *responseBodyCopier) FirstWriteTime() time.Time { return w.firstWrite }
