package server

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/server/usage"
	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
	"github.com/mostlygeek/llama-swap/internal/tailcat"
	"github.com/tidwall/gjson"
)

type TokenMetrics = store.TokenMetrics
type ActivityLogEntry = store.ActivityLogEntry

// ActivityLogEvent carries a single activity log entry to event subscribers.
type ActivityLogEvent struct {
	Metrics ActivityLogEntry
}

func (e ActivityLogEvent) Type() uint32 {
	return swaputil.ActivityLogEventID
}

// metricsMonitor parses upstream responses for token statistics, stores
// activity in a store, and (when captures are enabled) stores
// zstd+CBOR-compressed request/response captures in the store's capture
// repository, bounded to maxCaptureBytes in total.
type metricsMonitor struct {
	store           store.Store
	maxMetrics      int
	logger          *logmon.Monitor
	enableCaptures  bool
	maxCaptureBytes int64
}

func newMetricsMonitor(logger *logmon.Monitor, maxMetrics int, captureBufferMB int, st store.Store) *metricsMonitor {
	if maxMetrics <= 0 {
		maxMetrics = 1000
	}
	mm := &metricsMonitor{
		logger:         logger,
		store:          st,
		maxMetrics:     maxMetrics,
		enableCaptures: captureBufferMB > 0,
	}
	if captureBufferMB > 0 {
		mm.maxCaptureBytes = int64(captureBufferMB) * 1024 * 1024
		// The store outlives a config reload, so apply a lowered
		// captureBuffer now rather than on the next capture.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := st.Captures().Prune(ctx, mm.maxCaptureBytes); err != nil {
			mm.warnf("failed to prune captures: %v", err)
		}
	}
	return mm
}

// queueMetrics persists a metric and returns the store-assigned row. It
// deliberately does not take the request context: record runs after the
// handler returns, and an aborted request (canceled context) must still be
// recorded — that is exactly when the error entry matters.
func (mp *metricsMonitor) queueMetrics(metric ActivityLogEntry) (ActivityLogEntry, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stored, err := mp.store.Activity().Insert(ctx, metric)
	if err != nil {
		mp.warnf("failed to persist activity metric: %v", err)
		return ActivityLogEntry{}, false
	}
	if mp.store.IsInMemory() {
		if err := mp.store.Activity().Prune(ctx, mp.maxMetrics); err != nil {
			mp.warnf("failed to prune activity metrics: %v", err)
		}
	}
	return stored, true
}

// emitMetric publishes an ActivityLogEvent for the given metric.
func (mp *metricsMonitor) emitMetric(metric ActivityLogEntry) {
	event.Emit(ActivityLogEvent{Metrics: metric})
}

// overlayCaptureState sets HasCapture on entries whose capture is still in
// the capture repository. Stored captures are hidden while captures are
// disabled.
func (mp *metricsMonitor) overlayCaptureState(entries []ActivityLogEntry) {
	var found map[string]bool
	if mp.enableCaptures {
		ids := make([]string, 0, len(entries))
		for i := range entries {
			if entries[i].CaptureID != "" {
				ids = append(ids, entries[i].CaptureID)
			}
		}
		if len(ids) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var err error
			if found, err = mp.store.Captures().Has(ctx, ids); err != nil {
				mp.warnf("failed to look up captures: %v", err)
			}
		}
	}
	for i := range entries {
		entries[i].HasCapture = entries[i].CaptureID != "" && found[entries[i].CaptureID]
	}
}

// newCaptureID returns a capture ID for an activity row that will get a
// capture, or "" when captures are disabled. The ID is recorded on the row
// before the capture is written; if writing fails or the capture is pruned
// later, the row simply has no capture.
func (mp *metricsMonitor) newCaptureID() string {
	if !mp.enableCaptures {
		return ""
	}
	id, err := store.NewCaptureID()
	if err != nil {
		mp.warnf("skipping capture: %v", err)
		return ""
	}
	return id
}

func (mp *metricsMonitor) warnf(format string, args ...any) {
	if mp.logger != nil {
		mp.logger.Warnf(format, args...)
	}
}

func (mp *metricsMonitor) debugf(format string, args ...any) {
	if mp.logger != nil {
		mp.logger.Debugf(format, args...)
	}
}

func (mp *metricsMonitor) Close() error {
	return nil
}

// activitySource returns connection metadata for activity records. It
// prefers Tailcat's authenticated source, then falls back to the
// X-Forwarded-For/X-Real-IP headers (prefixed with "xff:" since a proxy
// header is client-supplied and can be spoofed), and finally the raw
// connection address.
func activitySource(r *http.Request) string {
	if source, ok := tailcat.SourceFromContext(r.Context()); ok {
		return source
	}
	if ip, ok := forwardedIP(r); ok {
		return "xff:" + ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

// record parses a completed response body and stores/emits an activity entry.
// Successful requests store a zstd+CBOR capture (when enabled) with cf
// controlling which parts are retained. Failed (non-200) requests capture the
// request only and set ErrorMsg to a description of the failure, so the error
// can be inspected without storing unreadable raw response bytes. reqBody and
// reqHeaders are the request data buffered before dispatch. task is the kind
// of inference, from the metered route table.
func (mp *metricsMonitor) record(modelID string, task hint.Task, r *http.Request, recorder *responseBodyCopier, cf captureFields, reqBody []byte, reqHeaders map[string]string) {
	tm := ActivityLogEntry{
		Timestamp:       time.Now(),
		Model:           modelID,
		Task:            string(task),
		ReqPath:         r.URL.Path,
		RespContentType: recorder.Header().Get("Content-Type"),
		RespStatusCode:  recorder.Status(),
		DurationMs:      int(time.Since(recorder.StartTime()).Milliseconds()),
		Tokens:          TokenMetrics{ReasoningTokens: -1},
	}
	tm.Src = activitySource(r)

	// The router or peer proxy fills the hint while dispatching; it says
	// where the request went and which extractor understands the response.
	upstream := hint.Unknown
	if h := hint.From(r.Context()); h != nil {
		tm.Route = string(h.Route)
		tm.PeerID = h.PeerID
		upstream = h.Upstream
	}

	if ctxData, ok := swaputil.ReadContext(r.Context()); ok && len(ctxData.Metadata) > 0 {
		tm.Metadata = make(map[string]string, len(ctxData.Metadata))
		for k, v := range ctxData.Metadata {
			tm.Metadata[k] = v
		}
	}
	if selectorID := selectorFromContext(r.Context()); selectorID != "" {
		if tm.Metadata == nil {
			tm.Metadata = make(map[string]string)
		}
		tm.Metadata["selector"] = selectorID
	}

	queueAndEmit := func() {
		stored, ok := mp.queueMetrics(tm)
		if !ok {
			return
		}
		tm = stored
		mp.emitMetric(tm)
	}

	// A client that hangs up is normal traffic, not a server fault: record the
	// entry so the abort is visible, but at debug level and without the
	// synthesized upstream error message the failure path below would attach.
	// No capture is stored — there is no response to inspect, and a client
	// retrying in a loop would otherwise flood the capture store. See #1029.
	if recorder.Status() == swaputil.StatusClientClosedRequest {
		mp.debugf("metrics: client disconnected before response, path=%s", r.URL.Path)
		tm.ErrorMsg = "client disconnected before response"
		queueAndEmit()
		return
	}

	if recorder.Status() != http.StatusOK {
		mp.logger.Warnf("non-200 response, recording partial metrics: status=%d, path=%s", recorder.Status(), r.URL.Path)
		decoded, decErr := mp.decodeResponseBody(recorder, r.URL.Path)
		tm.ErrorMsg = failedErrorMessage(recorder.Status(), decoded, decErr)
		tm.CaptureID = mp.newCaptureID()
		stored, ok := mp.queueMetrics(tm)
		if !ok {
			return
		}
		tm = stored
		// Capture the request only; the failure is surfaced via ErrorMsg
		// rather than storing the (possibly undisplayable) response body.
		tm.HasCapture = mp.storeCapture(tm, r, recorder, cf&^captureRespBody, reqBody, reqHeaders, nil)
		mp.emitMetric(tm)
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
			tm.ErrorMsg = fmt.Sprintf("response decompression failed: %v", err)
			queueAndEmit()
			return
		}
		body = decoded
	}

	if u, err := usage.Extract(upstream, r.URL.Path, recorder.Header().Get("Content-Type"), body, recorder.StartTime()); err != nil {
		mp.logger.Warnf("metrics: %v, path=%s, recording minimal metrics", err, r.URL.Path)
	} else if u.Found {
		tm.Tokens = u.Tokens
		tm.DurationMs = u.DurationMs
	}

	tm.CaptureID = mp.newCaptureID()
	stored, ok := mp.queueMetrics(tm)
	if !ok {
		return
	}
	tm = stored
	tm.HasCapture = mp.storeCapture(tm, r, recorder, cf, reqBody, reqHeaders, body)
	mp.emitMetric(tm)
}

// storeCapture assembles a ReqRespCapture for the stored activity row tm,
// honoring the captureFields mask, and stores it under tm.CaptureID when one
// was assigned. body is the response body to
// capture (already decompressed by the caller); pass nil to omit it. Returns
// true if a capture was stored.
func (mp *metricsMonitor) storeCapture(tm ActivityLogEntry, r *http.Request, recorder *responseBodyCopier, cf captureFields, reqBody []byte, reqHeaders map[string]string, body []byte) bool {
	if !mp.enableCaptures || tm.CaptureID == "" {
		return false
	}
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
	return mp.addCapture(tm.CaptureID, capture)
}

// decodeResponseBody returns the buffered response body, decompressing it when
// the upstream set a Content-Encoding we recognize. On decompression failure it
// logs a warning and returns an error so the caller can record a description
// (via ErrorMsg) instead of storing unreadable raw bytes.
func (mp *metricsMonitor) decodeResponseBody(recorder *responseBodyCopier, path string) ([]byte, error) {
	body := recorder.body.Bytes()
	if len(body) == 0 {
		return nil, nil
	}
	encoding := recorder.Header().Get("Content-Encoding")
	if encoding == "" {
		return body, nil
	}
	decoded, err := decompressBody(body, encoding)
	if err != nil {
		mp.logger.Warnf("metrics: response decompression failed: %v, path=%s", err, path)
		return nil, err
	}
	return decoded, nil
}

// errorMessagePaths lists JSON paths where a human-readable error message can
// live across OpenAI- and llama.cpp-style error responses.
var errorMessagePaths = []string{"error.message", "error", "message", "detail"}

// extractErrorMessage pulls a human-readable error string from a JSON error
// response. Returns "" if no message is found or the body is not valid JSON.
func extractErrorMessage(body []byte) string {
	if !gjson.ValidBytes(body) {
		return ""
	}
	parsed := gjson.ParseBytes(body)
	for _, path := range errorMessagePaths {
		v := parsed.Get(path)
		if v.Exists() && v.Type == gjson.String {
			if s := strings.TrimSpace(v.String()); s != "" {
				return s
			}
		}
	}
	return ""
}

// failedErrorMessage builds a human-readable description for a non-200 response.
// It prefers an error message parsed from the (decompressed) body and falls back
// to the HTTP status text. A non-nil decErr indicates the body could not be
// decoded, in which case the decode error is described instead.
func failedErrorMessage(status int, body []byte, decErr error) string {
	const maxLen = 500
	if decErr != nil {
		return fmt.Sprintf("response decode failed: %v", decErr)
	}
	if msg := extractErrorMessage(body); msg != "" {
		if len(msg) > maxLen {
			msg = msg[:maxLen] + "..."
		}
		return msg
	}
	if text := http.StatusText(status); text != "" {
		return fmt.Sprintf("%d %s", status, text)
	}
	return fmt.Sprintf("HTTP %d", status)
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
	// On a protocol upgrade (e.g. websocket) the body is raw framed data, not a
	// metrics-parseable response, so write straight to the client without
	// buffering a copy we can't use.
	if w.status == http.StatusSwitchingProtocols {
		return w.ResponseWriter.Write(b)
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

// MarkStatus records code for metrics without writing to the client, and
// forwards to the wrapped writer so the access-log recorder agrees.
func (w *responseBodyCopier) MarkStatus(code int) {
	w.status = code
	if marker, ok := w.ResponseWriter.(swaputil.StatusMarker); ok {
		marker.MarkStatus(code)
	}
}

// WroteHeader reports whether a response status reached the client.
func (w *responseBodyCopier) WroteHeader() bool { return w.wroteHeader }

// Flush forwards to the underlying writer so streaming responses still flush.
func (w *responseBodyCopier) Flush() {
	f, ok := w.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	// Flushing commits the implicit 200, so the response has started even if
	// nothing wrote a header. Recorded here for the same reason as in
	// statusRecorder: the client-closed sentinel must not overwrite it.
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	f.Flush()
}

// Hijack forwards to the underlying writer so httputil.ReverseProxy can take
// over the connection for websocket upgrades.
func (w *responseBodyCopier) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
}

func (w *responseBodyCopier) Status() int          { return w.status }
func (w *responseBodyCopier) StartTime() time.Time { return w.start }
