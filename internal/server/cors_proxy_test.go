package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// newLlamaServerStub returns an upstream that behaves like llama-server: it
// sets Access-Control-Allow-Origin to whatever Origin the client sent, so the
// header is present-but-empty when the client sent none. That empty value is
// the trailing half of the invalid "*, " from issue #85.
func newLlamaServerStub(t *testing.T) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

// newProxyingTestServer builds a Server whose local router is a real
// httputil.ReverseProxy pointed at upstreamURL, so the test exercises the
// actual header-merging behaviour rather than a stand-in. strip mirrors what
// the model and peer proxies do in production.
func newProxyingTestServer(t *testing.T, upstreamURL string, strip bool) *httptest.Server {
	t.Helper()

	target, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	if strip {
		proxy.ModifyResponse = func(resp *http.Response) error {
			swaputil.StripUpstreamCORSHeaders(resp.Header)
			return nil
		}
	}

	local := newStubRouter([]string{"m1"}, "")
	local.serveHTTP = proxy.ServeHTTP

	s := newTestServerWithConfig(config.Config{}, local, newStubRouter(nil, ""))
	front := httptest.NewServer(s)
	t.Cleanup(front.Close)
	return front
}

// postChat issues a real HTTP request so repeated header lines are parsed the
// way a client library would see them, which httptest.NewRecorder cannot show.
func postChat(t *testing.T, baseURL, origin string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions",
		strings.NewReader(`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	})
	return resp
}

// TestServer_CORSProxiedResponseIsSingleValued is the end-to-end regression
// test for issue #85. A proxied response must never carry more than one
// Access-Control-Allow-Origin, because clients that fold repeated headers see
// "*, " — an illegal field value that h11 rejects, killing the litellm
// request with no response at all.
func TestServer_CORSProxiedResponseIsSingleValued(t *testing.T) {
	upstream := newLlamaServerStub(t)
	front := newProxyingTestServer(t, upstream.URL, true)

	cases := []struct {
		name   string
		origin string
		want   string // expected single value, "" means the header must be absent
	}{
		{"no Origin, as curl and litellm send", "", ""},
		{"browser Origin", "http://example.com", "*"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postChat(t, front.URL, c.origin)

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d want 200", resp.StatusCode)
			}

			values := resp.Header.Values("Access-Control-Allow-Origin")
			if len(values) > 1 {
				t.Fatalf("Access-Control-Allow-Origin has %d values %q; a folding client sees %q",
					len(values), values, strings.Join(values, ", "))
			}

			if c.want == "" {
				if len(values) != 0 {
					t.Fatalf("Access-Control-Allow-Origin=%q want it absent when no Origin was sent", values)
				}
				return
			}
			if len(values) != 1 || values[0] != c.want {
				t.Fatalf("Access-Control-Allow-Origin=%q want exactly [%q]", values, c.want)
			}
		})
	}
}

// TestServer_CORSProxiedResponseHasNoEmptyHeaders guards the subtler half of
// #85: an upstream header that is present but empty. Folded with llama-swap's
// own it produces a value with trailing whitespace, which RFC 9110 forbids.
func TestServer_CORSProxiedResponseHasNoEmptyHeaders(t *testing.T) {
	upstream := newLlamaServerStub(t)
	front := newProxyingTestServer(t, upstream.URL, true)

	for _, origin := range []string{"", "http://example.com"} {
		resp := postChat(t, front.URL, origin)
		for name, values := range resp.Header {
			if !strings.HasPrefix(http.CanonicalHeaderKey(name), "Access-Control-") {
				continue
			}
			for _, v := range values {
				if strings.TrimSpace(v) == "" {
					t.Errorf("Origin=%q: %s is present but empty", origin, name)
				}
				if v != strings.TrimSpace(v) {
					t.Errorf("Origin=%q: %s=%q has leading or trailing whitespace", origin, name, v)
				}
			}
		}
	}
}

// TestServer_CORSReverseProxyWouldDuplicateWithoutStrip documents why
// swaputil.StripUpstreamCORSHeaders is load-bearing rather than defensive.
// httputil.ReverseProxy merges upstream headers with Header.Add, so without the
// strip a cross-origin response carries both llama-swap's value and the
// upstream's. If this test ever stops reproducing the duplicate, Go's proxy
// semantics changed and the strip's justification should be revisited.
func TestServer_CORSReverseProxyWouldDuplicateWithoutStrip(t *testing.T) {
	upstream := newLlamaServerStub(t)
	front := newProxyingTestServer(t, upstream.URL, false)

	resp := postChat(t, front.URL, "http://example.com")

	values := resp.Header.Values("Access-Control-Allow-Origin")
	if len(values) != 2 {
		t.Fatalf("Access-Control-Allow-Origin=%q; expected the un-stripped proxy to send 2 values", values)
	}
	if folded := strings.Join(values, ", "); folded != "*, http://example.com" {
		t.Errorf("folded value = %q want %q", folded, "*, http://example.com")
	}
}
