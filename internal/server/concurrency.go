package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/router"
)

// defaultConcurrencyLimit caps simultaneous in-flight requests per model when
// the model config leaves concurrencyLimit unset. Matches the legacy
// proxy.Process default.
const defaultConcurrencyLimit = 10

type callerCtxKey struct{}

// CreateCallerMiddleware captures the caller id (the presented API key) into the
// request context before the auth middleware strips the auth headers. The
// scheduler uses it to look up the caller's priority. It is a no-op for behavior
// when scheduling is disabled; it only annotates the context.
func CreateCallerMiddleware() chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// extractAPIKey (from auth.go) is read-only; auth strips the headers
			// downstream. Same key the auth middleware validates against.
			if id := extractAPIKey(r); id != "" {
				r = r.WithContext(contextWithCaller(r.Context(), id))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func contextWithCaller(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, callerCtxKey{}, id)
}

func callerFromContext(ctx context.Context) string {
	id, _ := ctx.Value(callerCtxKey{}).(string)
	return id
}

// CreateConcurrencyMiddleware returns middleware that limits simultaneous
// model-dispatched requests per model.
//
// When config.Scheduling is enabled it uses the supplied priority scheduler:
// requests that cannot immediately acquire a slot are queued and admitted by
// caller priority, and rejected with 429 + Retry-After only when the queue is
// full or the wait would exceed maxWait.
//
// When scheduling is disabled it preserves the legacy behavior: each model gets
// a semaphore sized to its concurrencyLimit (or defaultConcurrencyLimit) and a
// request that cannot immediately acquire a slot is rejected with a bare 429.
//
// Models without a local config entry (e.g. peer-routed models) are not limited.
func CreateConcurrencyMiddleware(cfg config.Config, sched *scheduler, logger *logmon.Monitor, mm *metricsMonitor) chain.Middleware {
	if cfg.Scheduling.Enabled() && sched != nil {
		return createSchedulingMiddleware(cfg, sched, logger, mm)
	}

	semaphores := make(map[string]*semaphore.Weighted, len(cfg.Models))
	for id, mc := range cfg.Models {
		limit := defaultConcurrencyLimit
		if mc.ConcurrencyLimit > 0 {
			limit = mc.ConcurrencyLimit
		}
		semaphores[id] = semaphore.NewWeighted(int64(limit))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, err := router.FetchContext(r, cfg)
			if err != nil {
				router.SendError(w, r, router.ErrNoModelInContext)
				return
			}

			// fall through for peer models
			sem, ok := semaphores[data.ModelID]
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			if !sem.TryAcquire(1) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"Too many requests"}`))
				return
			}
			defer sem.Release(1)
			next.ServeHTTP(w, r)
		})
	}
}

// createSchedulingMiddleware is the priority-aware admission path.
func createSchedulingMiddleware(cfg config.Config, sched *scheduler, logger *logmon.Monitor, mm *metricsMonitor) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, err := router.FetchContext(r, cfg)
			if err != nil {
				router.SendError(w, r, router.ErrNoModelInContext)
				return
			}

			q := sched.queueFor(data.ModelID)
			if q == nil {
				next.ServeHTTP(w, r) // unmanaged (peer) model
				return
			}

			callerID := callerFromContext(r.Context())
			priority := cfg.Scheduling.PriorityFor(callerID, data.ModelID)

			// For streaming chat requests with loading-state enabled, surface the
			// live queue position in the reasoning ("thinking") stream while the
			// request waits for a slot — the admission-queue analog of the swap
			// queue's position display. The stream is created lazily on the first
			// position, so a request admitted immediately (or rejected at entry,
			// before it parks) emits nothing and the normal response / 429 path is
			// preserved.
			var posCh chan int
			var stopWait func()
			if data.Streaming && data.SendLoadingState && router.IsLoadingPath(r.URL.Path) {
				posCh = make(chan int, 1)
				stopWait = router.StreamQueueWait(logger, data.ModelID, w, r, posCh)
			}

			a := sched.enqueue(r.Context(), data.ModelID, callerID, priority, posCh)
			if stopWait != nil {
				stopWait()
			}
			if !a.admitted {
				if logger != nil {
					who := callerID
					if who == "" {
						who = "(anonymous)"
					}
					// A caller that fans out wider than a model's concurrency
					// queues behind itself; once the queue/wait cap trips it
					// 429s on its own traffic. Surface the numbers so the
					// "self-interference" pattern is visible in the log.
					logger.Warnf("scheduler 429 model=%s caller=%s reason=%s concurrency=%d inflight=%d waiting=%d retry_after=%ds",
						data.ModelID, who, a.reason, a.concurrency, a.inflight, a.waiting, retryAfterSecs(a.retryAfter))
				}
				// Surface the rejection in the activity/trace stream too — a
				// scheduler 429 never reaches the metrics middleware, so this is
				// the only point it can be recorded (no double-count). The hint
				// headers+body double as the capture's response side.
				headers, body := backpressureResponse(data.ModelID, a)
				mm.recordRejection(data.ModelID, callerID, r.URL.Path, http.StatusTooManyRequests, headers, body)
				writeBackpressure(w, headers, body)
				return
			}

			start := time.Now()
			defer func() {
				a.release()
				q.observe(time.Since(start))
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// retryAfterSecs renders a duration as whole seconds (RFC 7231 delta-seconds),
// floored at 1.
func retryAfterSecs(d time.Duration) int {
	secs := int(d.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return secs
}

// backpressureResponse builds the 429's hint headers and JSON body once, so the
// same content can be both written to the client and stored as a capture.
//
// hint.max_concurrency is the actionable number: cap your client's parallelism
// (or batch) at this and you stop competing with yourself. The X-RateLimit-*
// headers carry the same numbers for header-only clients.
func backpressureResponse(model string, a admission) (headers map[string]string, body []byte) {
	secs := retryAfterSecs(a.retryAfter)
	headers = map[string]string{
		"Content-Type":         "application/json",
		"Retry-After":          strconv.Itoa(secs),
		"X-RateLimit-Limit":    strconv.Itoa(a.concurrency), // model's hard slot count
		"X-RateLimit-Inflight": strconv.Itoa(a.inflight),    // slots in use
		"X-RateLimit-Waiting":  strconv.Itoa(a.waiting),     // already queued
	}
	body = []byte(fmt.Sprintf(
		`{"error":"Too many requests","reason":%q,"retry_after":%d,"model":%q,"hint":{"max_concurrency":%d,"inflight":%d,"waiting":%d}}`,
		a.reason, secs, model, a.concurrency, a.inflight, a.waiting))
	return headers, body
}

// writeBackpressure emits a 429 carrying the model's concurrency so a client
// can right-size its executor.
func writeBackpressure(w http.ResponseWriter, headers map[string]string, body []byte) {
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write(body)
}
