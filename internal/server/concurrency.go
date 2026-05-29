package server

import (
	"net/http"

	"golang.org/x/sync/semaphore"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/router"
)

// defaultConcurrencyLimit caps simultaneous in-flight requests per model when
// the model config leaves concurrencyLimit unset. Matches the legacy
// proxy.Process default.
const defaultConcurrencyLimit = 10

// CreateConcurrencyMiddleware returns middleware that limits simultaneous
// model-dispatched requests per model. Each model gets a semaphore sized to
// its concurrencyLimit (or defaultConcurrencyLimit). A request that cannot
// immediately acquire a slot is rejected with 429. Models without a local
// config entry (e.g. peer-routed models) are not limited.
func CreateConcurrencyMiddleware(cfg config.Config) chain.Middleware {
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
				http.Error(w, "Too many requests", http.StatusTooManyRequests)
				return
			}
			defer sem.Release(1)
			next.ServeHTTP(w, r)
		})
	}
}
