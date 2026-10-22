package swaputil

import (
	"net/http"
	"strings"
)

// corsResponseHeaders are the CORS headers an upstream may set on its own
// responses. llama-swap's CORS middleware has already set its values on the
// ResponseWriter by the time a proxied response comes back, and
// httputil.ReverseProxy merges upstream headers with Header.Add rather than
// Header.Set, so leaving these in place sends two values under one key. Strict
// clients fold them: litellm/h11 rejects the resulting "*, " with
// "Illegal header value" and drops the connection. See issues #85 and #1133.
var corsResponseHeaders = []string{
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Methods",
	"Access-Control-Allow-Headers",
	"Access-Control-Allow-Credentials",
	"Access-Control-Expose-Headers",
	"Access-Control-Max-Age",
	"Access-Control-Allow-Private-Network",
}

// browserRequestHeaders are headers a browser attaches to cross-origin
// requests. They describe the hop from the browser to llama-swap, not the hop
// from llama-swap to an upstream.
var browserRequestHeaders = []string{
	"Origin",
	"Referer",
	"Access-Control-Request-Method",
	"Access-Control-Request-Headers",
	"Access-Control-Request-Private-Network",
}

// StripBrowserRequestHeaders deletes the browser-only headers (Origin,
// Referer, Sec-Fetch-* and the CORS preflight request headers) from a request
// about to be sent upstream.
//
// llama-swap's CORS middleware already answered the browser, so these only
// matter to an upstream that treats them as a policy signal. Anthropic's API
// rejects any request carrying Origin unless it also sets
// anthropic-dangerous-direct-browser-access. See issue #1214.
func StripBrowserRequestHeaders(h http.Header) {
	for _, name := range browserRequestHeaders {
		h.Del(name)
	}
	for name := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "Sec-Fetch-") {
			delete(h, name)
		}
	}
}

// StripUpstreamCORSHeaders deletes every CORS header an upstream set so that
// llama-swap's CORS middleware remains the single source of these headers.
//
// Call it from httputil.ReverseProxy.ModifyResponse, which runs before the
// proxy copies the upstream headers onto the client response. It must run on
// every proxied response, not only cross-origin ones: llama-server echoes the
// request Origin unconditionally, so with no Origin it sends the header
// present-but-empty, which is the trailing half of the invalid "*, " value.
func StripUpstreamCORSHeaders(h http.Header) {
	for _, name := range corsResponseHeaders {
		h.Del(name)
	}
}
