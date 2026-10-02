package server

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// bodyKindForPath returns the body format a model-dispatched POST endpoint
// expects: multipart for the form routes, JSON for everything else.
func bodyKindForPath(path string) swaputil.BodyKind {
	if slices.Contains(modelPostFormRoutes, path) {
		return swaputil.BodyMultipart
	}
	return swaputil.BodyJSON
}

// CreateContentTypeMiddleware returns middleware that tolerates a missing or
// wrong Content-Type on model-dispatched POST requests. A JSON body sent with
// another type (curl -d defaults to application/x-www-form-urlencoded) is
// relabelled application/json; unusable bodies are rejected with a 400 that
// names the expected format. See swaputil.NormalizeBodyContentType.
func CreateContentTypeMiddleware() chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > swaputil.MaxRequestBodySize {
				swaputil.SendError(w, r, &swaputil.RequestBodyError{
					Message: fmt.Sprintf("request body exceeds the %d MB limit", swaputil.MaxRequestBodySize>>20),
					Status:  http.StatusRequestEntityTooLarge,
				})
				return
			}
			r, err := swaputil.BufferRequestBody(w, r)
			if err == nil {
				err = swaputil.NormalizeBodyContentType(r, bodyKindForPath(r.URL.Path))
			}
			if err != nil {
				swaputil.SendError(w, r, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
