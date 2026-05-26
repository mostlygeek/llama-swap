package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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
	h := CreateConcurrencyMiddleware(cfg)(final)

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
	h := CreateConcurrencyMiddleware(cfg)(final)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, concurrencyTestReq("peer-model"))
	if w.Code != http.StatusOK || called != 1 {
		t.Fatalf("unconfigured model: status=%d called=%d, want 200/1", w.Code, called)
	}
}
