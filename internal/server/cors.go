package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
)

// corsPolicy is the resolved, immutable CORS configuration. Building it once
// at startup keeps the per-request path to map lookups and header writes.
type corsPolicy struct {
	// allowAnyOrigin is set when allowedOrigins contains "*". The literal "*"
	// is then sent back, which is what browsers expect for a public API.
	allowAnyOrigin bool

	// allowedOrigins holds the lowercased origins allowed when
	// allowAnyOrigin is false. The matching origin is echoed verbatim.
	allowedOrigins map[string]string

	allowCredentials bool
	allowedMethods   string   // pre-joined header value
	allowedHeaders   []string // empty means echo the request's
	exposedHeaders   string   // pre-joined, empty means omit the header
	maxAge           string   // pre-formatted seconds
}

// newCORSPolicy resolves cfg into a corsPolicy.
//
// A nil cfg means the config declared no security.cors block, which selects
// the permissive policy llama-swap had before the setting existed: any origin
// allowed. A declared block always lists its own origins — config validation
// rejects one that does not — so nothing here widens access beyond it. The
// preflight mechanics still take their defaults either way, so a block that
// only names origins keeps working for a browser.
func newCORSPolicy(cfg *config.CORSConfig) corsPolicy {
	if cfg == nil {
		cfg = &config.CORSConfig{AllowedOrigins: config.DefaultCORSAllowedOrigins()}
	}
	origins := cfg.AllowedOrigins
	p := corsPolicy{
		allowedOrigins:   make(map[string]string, len(origins)),
		allowCredentials: cfg.AllowCredentials,
		allowedHeaders:   cfg.AllowedHeaders,
	}
	for _, origin := range origins {
		if origin == config.CORSWildcardOrigin {
			p.allowAnyOrigin = true
			continue
		}
		p.allowedOrigins[strings.ToLower(origin)] = origin
	}

	methods := cfg.AllowedMethods
	if len(methods) == 0 {
		methods = config.DefaultCORSAllowedMethods()
	}
	p.allowedMethods = strings.Join(methods, ", ")

	p.exposedHeaders = strings.Join(cfg.ExposedHeaders, ", ")

	maxAge := cfg.MaxAge
	if maxAge == 0 {
		maxAge = config.DefaultCORSMaxAge
	}
	p.maxAge = strconv.Itoa(maxAge)

	return p
}

// resolveOrigin returns the value for Access-Control-Allow-Origin and whether
// the request is one llama-swap answers with CORS headers at all.
//
// A request without an Origin is never one: only browsers send it, and adding
// the headers for everyone else is what reintroduced issue #85 — llama-server
// sets its own Access-Control-Allow-Origin, so the client received two values
// and strict clients (litellm/h11) rejected the folded "*, ".
func (p corsPolicy) resolveOrigin(origin string) (string, bool) {
	if origin == "" {
		return "", false
	}
	if p.allowAnyOrigin {
		// Credentials cannot be combined with "*"; config validation rejects
		// that pairing, so the wildcard is always safe to send here.
		return config.CORSWildcardOrigin, true
	}
	if allowed, ok := p.allowedOrigins[strings.ToLower(origin)]; ok {
		return allowed, true
	}
	return "", false
}

// addVary appends token to the Vary header, keeping it to a single line so
// clients and caches never have to fold repeated values themselves.
func addVary(h http.Header, token string) {
	if existing := h.Get("Vary"); existing != "" {
		h.Set("Vary", existing+", "+token)
		return
	}
	h.Set("Vary", token)
}

// writeSharedHeaders writes the headers that belong on both preflight and
// actual responses.
func (p corsPolicy) writeSharedHeaders(h http.Header, allowOrigin string) {
	h.Set("Access-Control-Allow-Origin", allowOrigin)
	// Even the wildcard response varies by Origin now, because a request
	// without one gets no CORS headers at all.
	addVary(h, "Origin")
	if p.allowCredentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	}
	if p.exposedHeaders != "" {
		h.Set("Access-Control-Expose-Headers", p.exposedHeaders)
	}
}

// writePreflightHeaders adds the headers that only belong on a preflight
// response.
func (p corsPolicy) writePreflightHeaders(h http.Header, r *http.Request) {
	h.Set("Access-Control-Allow-Methods", p.allowedMethods)

	switch {
	case len(p.allowedHeaders) > 0:
		h.Set("Access-Control-Allow-Headers", strings.Join(p.allowedHeaders, ", "))
	default:
		requested := r.Header.Get("Access-Control-Request-Headers")
		if sanitized := sanitizeAccessControlRequestHeaderValues(requested); sanitized != "" {
			h.Set("Access-Control-Allow-Headers", sanitized)
			addVary(h, "Access-Control-Request-Headers")
		} else {
			h.Set("Access-Control-Allow-Headers", strings.Join(config.DefaultCORSAllowedHeaders(), ", "))
		}
	}

	h.Set("Access-Control-Max-Age", p.maxAge)
}

// CreateCORSMiddleware returns middleware that answers OPTIONS preflight
// requests (see issues #81, #77, #42) and adds CORS headers to the actual
// response so browser dashboards can read it (see #1121).
//
// Headers are only written when the request carries an Origin. Clients that
// send none — curl, litellm and most SDKs — get a response with no
// Access-Control-* header at all. That, together with
// swaputil.StripUpstreamCORSHeaders in the proxies, keeps llama-swap the only
// source of these headers and prevents the duplicate-header fold of issue #85.
//
// It must stay the outermost middleware after request logging so a preflight
// short-circuits before auth: browsers never attach credentials to a preflight.
func CreateCORSMiddleware(cfg config.Config) chain.Middleware {
	policy := newCORSPolicy(cfg.Security.CORS)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowOrigin, allowed := policy.resolveOrigin(r.Header.Get("Origin"))

			if r.Method != http.MethodOptions {
				if allowed {
					policy.writeSharedHeaders(w.Header(), allowOrigin)
				}
				next.ServeHTTP(w, r)
				return
			}

			// Every OPTIONS is answered here rather than falling through to
			// the mux, which registers no OPTIONS routes. A disallowed or
			// absent origin still gets 204, just without the headers the
			// browser needs to proceed.
			if allowed {
				policy.writeSharedHeaders(w.Header(), allowOrigin)
				policy.writePreflightHeaders(w.Header(), r)
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
}

// sanitizeAccessControlRequestHeaderValues drops any header names that contain
// characters outside the HTTP token grammar before echoing them back.
func sanitizeAccessControlRequestHeaderValues(headerValues string) string {
	parts := strings.Split(headerValues, ",")
	valid := make([]string, 0, len(parts))

	for _, p := range parts {
		if v := strings.TrimSpace(p); config.IsHTTPToken(v) {
			valid = append(valid, v)
		}
	}

	return strings.Join(valid, ", ")
}
