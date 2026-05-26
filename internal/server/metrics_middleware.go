package server

import (
	"bytes"
	"io"
	"net/http"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/router"
)

// CreateMetricsMiddleware returns middleware that records token metrics for
// model-dispatched POST requests. It resolves the model, tees the response into
// a buffer, and parses token usage once the upstream handler returns.
func CreateMetricsMiddleware(mm *metricsMonitor, cfg config.Config) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mm == nil || r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}

			// Resolve the model now so downstream dispatch hits the context
			// fast path; FetchContext restores the request body.
			data, err := router.FetchContext(r, cfg)
			if err != nil {
				router.SendError(w, r, router.ErrNoModelInContext)
				return
			}

			// Buffer the request body/headers for capture before dispatch
			// consumes them.
			cf := captureFieldsFor(r.URL.Path)
			var reqBody []byte
			var reqHeaders map[string]string
			if mm.enableCaptures {
				if cf&captureReqBody != 0 && r.Body != nil {
					if buffered, err := io.ReadAll(r.Body); err == nil {
						reqBody = buffered
						r.Body.Close()
						r.Body = io.NopCloser(bytes.NewReader(reqBody))
					}
				}
				if cf&captureReqHeaders != 0 {
					reqHeaders = headerMap(r.Header)
					redactHeaders(reqHeaders)
				}
			}

			// Restrict Accept-Encoding to encodings we can decompress so the
			// buffered response body stays parseable.
			if ae := r.Header.Get("Accept-Encoding"); ae != "" {
				r.Header.Set("Accept-Encoding", filterAcceptEncoding(ae))
			}

			recorder := newBodyCopier(w)
			next.ServeHTTP(recorder, r)
			mm.record(data.ModelID, r, recorder, cf, reqBody, reqHeaders)
		})
	}
}
