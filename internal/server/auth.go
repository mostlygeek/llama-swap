package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// CreateAuthMiddleware returns middleware that validates API keys when the
// config declares any. It accepts the key via Authorization: Bearer,
// Authorization: Basic (password field), or x-api-key. When no keys are
// configured the middleware is a pass-through.
func CreateAuthMiddleware(cfg config.Config) chain.Middleware {
	keys := cfg.RequiredAPIKeys
	return func(next http.Handler) http.Handler {
		if len(keys) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided := swaputil.ExtractAPIKey(r)

			valid := false
			for _, key := range keys {
				if provided == key {
					valid = true
					break
				}
			}
			if !valid {
				w.Header().Set("WWW-Authenticate", `Basic realm="llama-swap"`)
				swaputil.SendResponse(w, r, http.StatusUnauthorized, "unauthorized: invalid or missing API key")
				return
			}
			if isCrossSiteBasicAuth(r) {
				swaputil.SendResponse(w, r, http.StatusForbidden, "forbidden: cross-site request with browser credentials")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isCrossSiteBasicAuth reports a request that carries Basic credentials and
// that another site started (GHSA-g4gp-qf3f-4x4w). After a user logs in through
// the browser's Basic-auth prompt, the browser attaches those credentials to
// every request to this host, including image loads and form posts started by
// any other web page. CORS keeps that page from reading responses, but the
// request still runs (unload, pause, a generation).
//
// Bearer and x-api-key credentials cannot be attached by another page without
// a CORS preflight, so they are not affected, nor are non-browser clients,
// which send neither Sec-Fetch-Site nor Origin.
func isCrossSiteBasicAuth(r *http.Request) bool {
	scheme, _, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "basic") {
		return false
	}

	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return false
	case "cross-site", "same-site":
		// A link from another site to the UI is fine; it only renders pages.
		if r.Method == http.MethodGet && r.Header.Get("Sec-Fetch-Mode") == "navigate" &&
			(r.URL.Path == "/ui" || strings.HasPrefix(r.URL.Path, "/ui/")) {
			return false
		}
		return true
	}

	// Browsers without Fetch Metadata: compare Origin with Host on requests
	// that change state.
	origin := r.Header.Get("Origin")
	if origin == "" || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	u, err := url.Parse(origin)
	return err != nil || !strings.EqualFold(u.Host, r.Host)
}

// CreateRequestContextMiddleware returns middleware that extracts model and
// auth info from the request into the context. Requests where no model can be
// identified are rejected with a 404, and bodies over the size cap with a 413.
//
// JSON and multipart bodies without a Content-Length are first read here, so
// this is where their size cap is usually hit.
func CreateRequestContextMiddleware(cfg config.Config) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = markInflightStart(r)
			data, err := swaputil.FetchContext(r, cfg)
			if err != nil {
				if bodyErr := bodyTooLargeFrom(err); bodyErr != nil {
					swaputil.SendResponse(w, r, bodyErr.status, bodyErr.message)
					return
				}
				swaputil.SendError(w, r, swaputil.ErrNoModelInContext)
				return
			}
			_ = data
			next.ServeHTTP(w, r)
		})
	}
}
