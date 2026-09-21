package swaputil

import (
	"net/http"
	"testing"
)

func TestStripUpstreamCORSHeaders(t *testing.T) {
	h := http.Header{}
	for _, name := range corsResponseHeaders {
		h.Set(name, "upstream-value")
	}
	// llama-server sends an empty Access-Control-Allow-Origin when the client
	// sent no Origin; ReverseProxy would add it alongside llama-swap's own,
	// producing the "*, " that strict clients reject. See issue #85.
	h.Add("Access-Control-Allow-Origin", "")
	h.Set("Content-Type", "application/json")
	h.Set("X-Accel-Buffering", "no")

	StripUpstreamCORSHeaders(h)

	for _, name := range corsResponseHeaders {
		if values := h.Values(name); len(values) != 0 {
			t.Errorf("%s survived with %q", name, values)
		}
	}
	if got := h.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type=%q, unrelated headers must be untouched", got)
	}
	if got := h.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering=%q, unrelated headers must be untouched", got)
	}
}

// TestStripUpstreamCORSHeaders_LowercaseKeys checks the helper works on headers
// built with non-canonical keys, which is how a raw map literal spells them.
func TestStripUpstreamCORSHeaders_LowercaseKeys(t *testing.T) {
	h := http.Header{}
	h.Add("access-control-allow-origin", "*")
	h.Add("ACCESS-CONTROL-ALLOW-ORIGIN", "http://example.com")

	StripUpstreamCORSHeaders(h)

	if values := h.Values("Access-Control-Allow-Origin"); len(values) != 0 {
		t.Errorf("Access-Control-Allow-Origin survived with %q", values)
	}
}

func TestStripUpstreamCORSHeaders_EmptyHeader(t *testing.T) {
	h := http.Header{}
	StripUpstreamCORSHeaders(h)
	if len(h) != 0 {
		t.Errorf("empty header gained entries: %v", h)
	}
}
