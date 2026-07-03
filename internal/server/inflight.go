package server

import (
	"context"
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

// inflightTracker tracks in-flight model-dispatched requests and their
// cancellable contexts.
type inflightTracker struct {
	nextID atomic.Uint64

	mu       sync.RWMutex
	requests map[string]inflightRequest
}

type inflightRequest struct {
	entry  shared.InflightRequestEntry
	cancel context.CancelFunc
}

func newInflightTracker() *inflightTracker {
	return &inflightTracker{requests: make(map[string]inflightRequest)}
}

func (t *inflightTracker) Add(r *http.Request, cancel context.CancelFunc) string {
	id := strconv.FormatUint(t.nextID.Add(1), 10)
	entry := shared.InflightRequestEntry{
		ID:        id,
		Timestamp: time.Now(),
		ReqPath:   r.URL.Path,
		Method:    r.Method,
	}
	if data, ok := shared.ReadContext(r.Context()); ok {
		entry.Model = data.ModelID
		entry.Metadata = copyMetadata(data.Metadata)
	}

	t.mu.Lock()
	t.requests[id] = inflightRequest{entry: entry, cancel: cancel}
	snapshot := t.snapshotLocked()
	t.mu.Unlock()

	event.Emit(snapshot)
	return id
}

func (t *inflightTracker) Remove(id string) {
	t.mu.Lock()
	delete(t.requests, id)
	snapshot := t.snapshotLocked()
	t.mu.Unlock()

	event.Emit(snapshot)
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
	return t.snapshotLocked()
}

func (t *inflightTracker) snapshotLocked() shared.InFlightRequestsEvent {
	requests := make([]shared.InflightRequestEntry, 0, len(t.requests))
	for _, req := range t.requests {
		requests = append(requests, req.entry)
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].Timestamp.Equal(requests[j].Timestamp) {
			return requests[i].ID < requests[j].ID
		}
		return requests[i].Timestamp.Before(requests[j].Timestamp)
	})
	return shared.InFlightRequestsEvent{Total: len(requests), Requests: requests}
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

			next.ServeHTTP(w, r)
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

			next.ServeHTTP(w, r)
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
