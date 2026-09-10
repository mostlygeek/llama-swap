package server

import (
	"net/http"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// CreateConcurrencyLimitMiddleware returns middleware that caps the number of
// inference requests served at once across all models, using a buffered
// channel as a semaphore. A request that cannot acquire a slot is rejected
// immediately with a 429 rather than queued (see issue #1086). Callers must
// not add this middleware when limit <= 0; there is no "no limit" mode here.
func CreateConcurrencyLimitMiddleware(limit int) chain.Middleware {
	sem := make(chan struct{}, limit)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				next.ServeHTTP(w, r)
			default:
				swaputil.SendError(w, r, swaputil.ConcurrencyLimitError{})
			}
		})
	}
}
