package config

import (
	"fmt"
	"net/url"
	"strings"
)

// CORSWildcardOrigin allows any origin. It is the default when
// security.cors.allowedOrigins is empty, which keeps an absent security block
// behaving exactly like llama-swap did before the setting existed.
const CORSWildcardOrigin = "*"

// DefaultCORSMaxAge is the Access-Control-Max-Age value sent on preflight
// responses when security.cors.maxAge is not set.
const DefaultCORSMaxAge = 86400

// DefaultCORSAllowedOrigins returns the default allowedOrigins list. The
// returned slice is fresh so callers may mutate it without affecting other
// configs.
func DefaultCORSAllowedOrigins() []string {
	return []string{CORSWildcardOrigin}
}

// DefaultCORSAllowedMethods returns the methods advertised on a preflight
// response when security.cors.allowedMethods is not set.
func DefaultCORSAllowedMethods() []string {
	return []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
}

// DefaultCORSAllowedHeaders returns the request headers advertised on a
// preflight response when the client sent no Access-Control-Request-Headers
// and security.cors.allowedHeaders is not set.
func DefaultCORSAllowedHeaders() []string {
	return []string{"Content-Type", "Authorization", "Accept", "X-Requested-With"}
}

// SecurityConfig groups settings that control who may talk to llama-swap.
type SecurityConfig struct {
	CORS CORSConfig `yaml:"cors"`
}

// CORSConfig controls the Access-Control-* headers llama-swap sends. The zero
// value is the historical permissive policy: any origin, no credentials. See
// issues #85 and #1121 — llama-swap must be the only source of these headers,
// because upstreams such as llama-server set their own and
// httputil.ReverseProxy adds rather than replaces them.
type CORSConfig struct {
	// AllowedOrigins lists the browser origins that may read responses.
	// Empty means DefaultCORSAllowedOrigins (any origin). A "*" entry cannot
	// be combined with AllowCredentials.
	AllowedOrigins []string `yaml:"allowedOrigins"`

	// AllowCredentials sends Access-Control-Allow-Credentials: true, letting
	// the browser attach cookies and Authorization headers.
	AllowCredentials bool `yaml:"allowCredentials"`

	// AllowedMethods is advertised on preflight responses. Empty means
	// DefaultCORSAllowedMethods.
	AllowedMethods []string `yaml:"allowedMethods"`

	// AllowedHeaders is advertised on preflight responses. Empty means the
	// client's sanitized Access-Control-Request-Headers is echoed back,
	// falling back to DefaultCORSAllowedHeaders when it sent none.
	AllowedHeaders []string `yaml:"allowedHeaders"`

	// ExposedHeaders lists response headers a browser may read beyond the
	// CORS-safelisted set. Empty omits the header.
	ExposedHeaders []string `yaml:"exposedHeaders"`

	// MaxAge is the preflight cache lifetime in seconds. 0 means
	// DefaultCORSMaxAge.
	MaxAge int `yaml:"maxAge"`
}

// Validate reports configuration that cannot produce a usable CORS policy.
func (c CORSConfig) Validate() error {
	for _, origin := range c.AllowedOrigins {
		if origin == CORSWildcardOrigin {
			if c.AllowCredentials {
				return fmt.Errorf(`allowedOrigins may not contain "*" when allowCredentials is true; list the origins explicitly`)
			}
			continue
		}
		if err := validateCORSOrigin(origin); err != nil {
			return err
		}
	}

	for _, m := range c.AllowedMethods {
		if !IsHTTPToken(m) {
			return fmt.Errorf("allowedMethods: %q is not a valid HTTP method", m)
		}
	}
	for _, h := range c.AllowedHeaders {
		if !IsHTTPToken(h) {
			return fmt.Errorf("allowedHeaders: %q is not a valid HTTP header name", h)
		}
	}
	for _, h := range c.ExposedHeaders {
		if !IsHTTPToken(h) {
			return fmt.Errorf("exposedHeaders: %q is not a valid HTTP header name", h)
		}
	}

	if c.MaxAge < 0 {
		return fmt.Errorf("maxAge must be >= 0")
	}

	return nil
}

// validateCORSOrigin checks that an entry is a bare scheme://host[:port], the
// only form a browser ever sends in an Origin header. A trailing slash or a
// path would silently never match, so reject it at load time.
func validateCORSOrigin(origin string) error {
	if origin == "" {
		return fmt.Errorf("allowedOrigins: entry must not be empty")
	}
	if origin != strings.TrimSpace(origin) {
		return fmt.Errorf("allowedOrigins: %q must not have leading or trailing whitespace", origin)
	}

	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("allowedOrigins: %q is not a valid origin: %w", origin, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("allowedOrigins: %q must be a scheme://host[:port] origin, e.g. https://dashboard.example.com", origin)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("allowedOrigins: %q must not include a path, query, fragment or userinfo", origin)
	}

	return nil
}

// IsHTTPToken reports whether s is a non-empty RFC 9110 token, the grammar
// both method names and header names follow. It is exported because
// internal/server sanitizes Access-Control-Request-Headers with the same rule
// and cannot import swaputil, which already depends on this package.
func IsHTTPToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isTokenChar(r) {
			return false
		}
	}
	return true
}

// isTokenChar reports whether r is valid in an RFC 9110 token.
func isTokenChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
	case r >= 'A' && r <= 'Z':
	case r >= '0' && r <= '9':
	case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
	default:
		return false
	}
	return true
}
