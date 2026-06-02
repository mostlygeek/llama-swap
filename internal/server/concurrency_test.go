package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/router"
)

func concurrencyTestReq(model string) *http.Request {
	r := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	return r.WithContext(router.SetContext(r.Context(), router.ReqContextData{Model: model, ModelID: model}))
}

func TestServer_ConcurrencyMiddleware_RejectsOverLimit(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"m1": {ConcurrencyLimit: 1},
		},
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := CreateConcurrencyMiddleware(cfg, nil, nil, nil)(final)

	// First request occupies the only slot.
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), concurrencyTestReq("m1"))
	}()
	<-entered

	// Second concurrent request is rejected with 429.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, concurrencyTestReq("m1"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit status = %d, want 429", w.Code)
	}

	// Once the slot frees, a new request succeeds.
	close(release)
	<-done
	w = httptest.NewRecorder()
	h.ServeHTTP(w, concurrencyTestReq("m1"))
	if w.Code != http.StatusOK {
		t.Fatalf("post-release status = %d, want 200", w.Code)
	}
}

func TestServer_ConcurrencyMiddleware_UnconfiguredModelPassesThrough(t *testing.T) {
	cfg := config.Config{Models: map[string]config.ModelConfig{}}

	called := 0
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	h := CreateConcurrencyMiddleware(cfg, nil, nil, nil)(final)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, concurrencyTestReq("peer-model"))
	if w.Code != http.StatusOK || called != 1 {
		t.Fatalf("unconfigured model: status=%d called=%d, want 200/1", w.Code, called)
	}
}

// TestWriteBackpressure_EmitsConcurrencyHint verifies the 429 carries the
// model's concurrency on both the body and X-RateLimit-* headers so a client
// can size its executor.
func TestWriteBackpressure_EmitsConcurrencyHint(t *testing.T) {
	rec := httptest.NewRecorder()
	headers, body := backpressureResponse("embed-model", admission{
		retryAfter:  2 * time.Second,
		reason:      "queue_full",
		concurrency: 1,
		inflight:    1,
		waiting:     8,
	})
	writeBackpressure(rec, headers, body)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Errorf("X-RateLimit-Limit = %q, want 1", got)
	}
	if got := rec.Header().Get("X-RateLimit-Waiting"); got != "8" {
		t.Errorf("X-RateLimit-Waiting = %q, want 8", got)
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want 2", got)
	}
	gotBody := rec.Body.String()
	for _, want := range []string{`"max_concurrency":1`, `"reason":"queue_full"`, `"model":"embed-model"`, `"waiting":8`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body missing %q; got %s", want, gotBody)
		}
	}
}
