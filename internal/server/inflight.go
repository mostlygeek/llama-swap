package server

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

const inflightUpdateInterval = 250 * time.Millisecond
const inflightOutboxSize = 128

const (
	inflightOperationSnapshot = "snapshot"
	inflightOperationUpsert   = "upsert"
	inflightOperationRemove   = "remove"
)

type inflightStartContextKey struct{}

func markInflightStart(r *http.Request) *http.Request {
	if _, ok := r.Context().Value(inflightStartContextKey{}).(time.Time); ok {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), inflightStartContextKey{}, time.Now()))
}

func inflightStart(r *http.Request) time.Time {
	if started, ok := r.Context().Value(inflightStartContextKey{}).(time.Time); ok {
		return started
	}
	return time.Now()
}

// inflightTracker tracks in-flight model-dispatched requests and their
// cancellable contexts.
type inflightTracker struct {
	nextID atomic.Uint64

	// mu serializes state mutations and their corresponding outbox writes so
	// request updates keep the same order in which they were applied.
	mu       sync.RWMutex
	requests map[string]*inflightRequest

	updates          chan shared.InFlightRequestsEvent
	needsSnapshot    atomic.Bool
	publisherRunning atomic.Bool
	publish          func(shared.InFlightRequestsEvent)
}

type inflightRequest struct {
	entry       shared.InflightRequestEntry
	cancel      context.CancelFunc
	lastEmitted time.Time
	timer       *time.Timer
}

func newInflightTracker() *inflightTracker {
	return newInflightTrackerWithPublisher(inflightOutboxSize, func(update shared.InFlightRequestsEvent) {
		event.Emit(update)
	})
}

func newInflightTrackerWithPublisher(size int, publish func(shared.InFlightRequestsEvent)) *inflightTracker {
	t := &inflightTracker{
		requests: make(map[string]*inflightRequest),
		updates:  make(chan shared.InFlightRequestsEvent, size),
		publish:  publish,
	}
	return t
}

func (t *inflightTracker) Add(r *http.Request, cancel context.CancelFunc) string {
	id := strconv.FormatUint(t.nextID.Add(1), 10)
	entry := shared.InflightRequestEntry{
		ID:          id,
		Timestamp:   inflightStart(r),
		ReqPath:     r.URL.Path,
		Method:      r.Method,
		ReqHeaders:  headerMap(r.Header),
		RemoteIP:    clientIP(r),
		RespHeaders: map[string]string{},
	}
	redactHeaders(entry.ReqHeaders)
	if data, ok := shared.ReadContext(r.Context()); ok {
		entry.Model = data.ModelID
		entry.Metadata = copyMetadata(data.Metadata)
	}

	t.mu.Lock()
	req := &inflightRequest{entry: entry, cancel: cancel, lastEmitted: time.Now()}
	t.requests[id] = req
	t.enqueueLocked(upsertInflightEvent(req.entry))
	t.mu.Unlock()
	return id
}

func (t *inflightTracker) Remove(id string) {
	t.mu.Lock()
	req, ok := t.requests[id]
	if ok {
		delete(t.requests, id)
		if req.timer != nil {
			req.timer.Stop()
		}
		t.enqueueLocked(shared.InFlightRequestsEvent{Operation: inflightOperationRemove, ID: id})
	}
	t.mu.Unlock()
}

func (t *inflightTracker) SetResponseHeaders(id string, headers http.Header) {
	values := headerMap(headers)
	redactHeaders(values)

	t.mu.Lock()
	req, ok := t.requests[id]
	if ok {
		req.entry.RespHeaders = values
		req.lastEmitted = time.Now()
		t.enqueueLocked(upsertInflightEvent(req.entry))
	}
	t.mu.Unlock()
}

func (t *inflightTracker) AddResponseBytes(id string, total int) {
	if total <= 0 {
		return
	}

	t.mu.Lock()
	req, ok := t.requests[id]
	if !ok {
		t.mu.Unlock()
		return
	}
	req.entry.RespBytes += int64(total)

	now := time.Now()
	remaining := inflightUpdateInterval - now.Sub(req.lastEmitted)
	if remaining <= 0 && req.timer == nil {
		req.lastEmitted = now
		t.enqueueLocked(upsertInflightEvent(req.entry))
		t.mu.Unlock()
		return
	}
	if req.timer == nil {
		if remaining < 0 {
			remaining = 0
		}
		req.timer = time.AfterFunc(remaining, func() { t.emitPending(id) })
	}
	t.mu.Unlock()
}

func (t *inflightTracker) emitPending(id string) {
	t.mu.Lock()
	req, ok := t.requests[id]
	if !ok {
		t.mu.Unlock()
		return
	}
	req.timer = nil
	req.lastEmitted = time.Now()
	t.enqueueLocked(upsertInflightEvent(req.entry))
	t.mu.Unlock()
}

// enqueueLocked adds an update without allowing event-bus backpressure to
// block the request path. On overflow, a later snapshot replaces any dropped
// incremental updates with the tracker's authoritative state.
func (t *inflightTracker) enqueueLocked(update shared.InFlightRequestsEvent) {
	select {
	case t.updates <- update:
	default:
		t.needsSnapshot.Store(true)
	}
	t.startPublisher()
}

func (t *inflightTracker) startPublisher() {
	if t.publisherRunning.CompareAndSwap(false, true) {
		go t.publishUpdates()
	}
}

func (t *inflightTracker) publishUpdates() {
	for {
		select {
		case update := <-t.updates:
			t.publish(refreshInflightElapsed(update))
			t.publishRecoverySnapshots()
		default:
			t.publisherRunning.Store(false)
			// An enqueue racing with the transition to idle either starts a new
			// publisher or leaves work here for this publisher to reclaim.
			if len(t.updates) > 0 && t.publisherRunning.CompareAndSwap(false, true) {
				continue
			}
			return
		}
	}
}

func (t *inflightTracker) publishRecoverySnapshots() {
	for t.needsSnapshot.Swap(false) {
		t.discardQueuedUpdates()
		t.publish(t.Current())
	}
}

func (t *inflightTracker) discardQueuedUpdates() {
	for {
		select {
		case <-t.updates:
		default:
			return
		}
	}
}

func (t *inflightTracker) Cancel(id string) bool {
	t.mu.RLock()
	req, ok := t.requests[id]
	t.mu.RUnlock()
	if !ok {
		return false
	}
	req.cancel()
	return true
}

func (t *inflightTracker) Current() shared.InFlightRequestsEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return shared.InFlightRequestsEvent{
		Operation: inflightOperationSnapshot,
		Requests:  t.snapshotLocked(),
	}
}

func (t *inflightTracker) snapshotLocked() []shared.InflightRequestEntry {
	requests := make([]shared.InflightRequestEntry, 0, len(t.requests))
	for _, req := range t.requests {
		requests = append(requests, copyInflightEntry(req.entry))
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].Timestamp.Equal(requests[j].Timestamp) {
			return requests[i].ID < requests[j].ID
		}
		return requests[i].Timestamp.Before(requests[j].Timestamp)
	})
	return requests
}

func upsertInflightEvent(entry shared.InflightRequestEntry) shared.InFlightRequestsEvent {
	entry = copyInflightEntry(entry)
	return shared.InFlightRequestsEvent{Operation: inflightOperationUpsert, Request: &entry}
}

func copyInflightEntry(entry shared.InflightRequestEntry) shared.InflightRequestEntry {
	entry.Metadata = copyMetadata(entry.Metadata)
	entry.ReqHeaders = copyStringMap(entry.ReqHeaders)
	entry.RespHeaders = copyStringMap(entry.RespHeaders)
	setInflightElapsed(&entry)
	return entry
}

func refreshInflightElapsed(update shared.InFlightRequestsEvent) shared.InFlightRequestsEvent {
	if update.Request != nil {
		entry := *update.Request
		setInflightElapsed(&entry)
		update.Request = &entry
	}
	for i := range update.Requests {
		setInflightElapsed(&update.Requests[i])
	}
	return update
}

func setInflightElapsed(entry *shared.InflightRequestEntry) {
	elapsed := time.Since(entry.Timestamp)
	if elapsed < 0 {
		elapsed = 0
	}
	entry.ElapsedMs = elapsed.Milliseconds()
}

func copyStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func copyMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for k, v := range metadata {
		out[k] = v
	}
	return out
}

type inflightResponseWriter struct {
	http.ResponseWriter
	tracker     *inflightTracker
	id          string
	wroteHeader bool
}

func (w *inflightResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.tracker.SetResponseHeaders(w.id, w.Header())
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *inflightResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.tracker.AddResponseBytes(w.id, n)
	return n, err
}

func (w *inflightResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *inflightResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
}

// CreateInflightMiddleware returns middleware that tracks model-dispatched
// requests until downstream handling completes.
func CreateInflightMiddleware(t *inflightTracker) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()

			r = r.WithContext(ctx)
			id := t.Add(r, cancel)
			defer t.Remove(id)

			next.ServeHTTP(&inflightResponseWriter{ResponseWriter: w, tracker: t, id: id}, r)
		})
	}
}

// CreateUpstreamInflightMiddleware tracks /upstream/<model>/<path> requests
// only when the stripped upstream path is one of the model-dispatched
// inference endpoints.
func CreateUpstreamInflightMiddleware(t *inflightTracker, cfg config.Config) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/upstream/") {
				next.ServeHTTP(w, r)
				return
			}
			r = markInflightStart(r)

			_, _, remainingPath, found := shared.FindModelInPath(cfg, strings.TrimPrefix(r.URL.Path, "/upstream"))
			if !found || !isModelDispatchedRequest(r.Method, remainingPath) {
				next.ServeHTTP(w, r)
				return
			}

			if _, err := shared.FetchContext(r, cfg); err != nil {
				next.ServeHTTP(w, r)
				return
			}

			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()

			r = r.WithContext(ctx)
			tracked := r.Clone(ctx)
			tracked.URL.Path = remainingPath
			id := t.Add(tracked, cancel)
			defer t.Remove(id)

			next.ServeHTTP(&inflightResponseWriter{ResponseWriter: w, tracker: t, id: id}, r)
		})
	}
}

func isModelDispatchedRequest(method, path string) bool {
	switch method {
	case http.MethodPost:
		for _, p := range modelPostJSONRoutes {
			if p == path {
				return true
			}
		}
		for _, p := range modelPostFormRoutes {
			if p == path {
				return true
			}
		}
	case http.MethodGet:
		for _, p := range modelGetRoutes {
			if p == path {
				return true
			}
		}
	}
	return false
}
