package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// corsHeaders are every response header the CORS policy may emit. Tests assert
// against the whole set rather than a single header so that a change which
// starts sending a new one unconditionally is caught.
var corsHeaders = []string{
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Methods",
	"Access-Control-Allow-Headers",
	"Access-Control-Allow-Credentials",
	"Access-Control-Expose-Headers",
	"Access-Control-Max-Age",
	"Access-Control-Allow-Private-Network",
}

// corsTestServer builds a Server whose security.cors is cfg. The zero
// CORSConfig names no origins, which selects the legacy permissive policy.
func corsTestServer(t *testing.T, cfg config.CORSConfig) *Server {
	t.Helper()
	// Mirror the load-time check so a test never exercises a policy the loader
	// would have rejected.
	if err := cfg.Validate(); err != nil {
		t.Fatalf("test CORS config is invalid: %v", err)
	}
	return newTestServerWithConfig(
		config.Config{Security: config.SecurityConfig{CORS: cfg}},
		newStubRouter([]string{"m1"}, "OK"),
		newStubRouter(nil, ""),
	)
}

// assertNoCORSHeaders fails when the recorder carries any Access-Control-*
// header. It walks the recorded header map rather than corsHeaders so a header
// no test knows about still trips it.
func assertNoCORSHeaders(t *testing.T, w *httptest.ResponseRecorder, context string) {
	t.Helper()
	for name, values := range w.Header() {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "Access-Control-") {
			t.Errorf("%s: unexpected %s=%q", context, name, values)
		}
	}
}

// TestServer_CORSNoOriginEmitsNoHeaders is the guard for issue #85. Clients
// that send no Origin — curl, litellm, most SDKs — must get a response with no
// CORS headers at all, so the upstream's copy can never be folded together
// with llama-swap's into an invalid "*, ".
func TestServer_CORSNoOriginEmitsNoHeaders(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{})

	requests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/running"},
		{http.MethodGet, "/v1/models"},
		{http.MethodGet, "/health"},
		{http.MethodPost, "/api/models/unload"},
	}

	for _, req := range requests {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(req.method, req.path, nil))
		assertNoCORSHeaders(t, w, fmt.Sprintf("%s %s", req.method, req.path))
	}
}

// TestServer_CORSHeadersAreSingleValued sweeps every registered route and
// asserts each CORS header carries at most one value, and never a folded or
// empty one. PR #1121 would have failed this once a proxied upstream added its
// own header.
func TestServer_CORSHeadersAreSingleValued(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{})

	requests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/running"},
		{http.MethodGet, "/v1/models"},
		{http.MethodGet, "/models"},
		{http.MethodGet, "/health"},
		{http.MethodGet, "/api/version"},
		{http.MethodPost, "/api/models/unload"},
		{http.MethodOptions, "/v1/chat/completions"},
		{http.MethodPost, "/v1/chat/completions"},
	}

	for _, origin := range []string{"", "http://example.com"} {
		for _, req := range requests {
			r := httptest.NewRequest(req.method, req.path, strings.NewReader(`{"model":"m1"}`))
			r.Header.Set("Content-Type", "application/json")
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)

			for _, name := range corsHeaders {
				values := w.Header().Values(name)
				where := fmt.Sprintf("%s %s (Origin=%q) %s", req.method, req.path, origin, name)
				if len(values) > 1 {
					t.Errorf("%s: %d values %q, want at most 1", where, len(values), values)
					continue
				}
				if len(values) == 1 && strings.TrimSpace(values[0]) == "" {
					t.Errorf("%s: present but empty", where)
				}
			}
		}
	}
}

// TestServer_CORSActualResponseWithOrigin covers the case PR #1121 was written
// for: a passing preflight is not enough, the browser needs the header on the
// real response too.
func TestServer_CORSActualResponseWithOrigin(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{})

	req := httptest.NewRequest(http.MethodGet, "/running", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin=%q want *", got)
	}
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary=%q want it to contain Origin", got)
	}
	// Preflight-only headers do not belong on an actual response.
	for _, name := range []string{"Access-Control-Allow-Methods", "Access-Control-Max-Age"} {
		if got := w.Header().Get(name); got != "" {
			t.Errorf("%s=%q want it absent on a non-preflight response", name, got)
		}
	}
}

func TestServer_CORSPreflight(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{})

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
	want := map[string]string{
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		"Access-Control-Allow-Headers": "Content-Type, Authorization, Accept, X-Requested-With",
		"Access-Control-Max-Age":       "86400",
	}
	for name, value := range want {
		if got := w.Header().Get(name); got != value {
			t.Errorf("%s=%q want %q", name, got, value)
		}
	}
}

func TestServer_CORSPreflightEchoesRequestHeaders(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{})

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Headers", "X-Custom, Content-Type")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "X-Custom, Content-Type" {
		t.Errorf("Access-Control-Allow-Headers=%q want the echoed request headers", got)
	}
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Access-Control-Request-Headers") {
		t.Errorf("Vary=%q want it to contain Access-Control-Request-Headers", got)
	}
}

// TestServer_CORSPreflightDropsIllegalRequestHeaders keeps the echo from
// turning a hostile request header into an illegal response header value.
func TestServer_CORSPreflightDropsIllegalRequestHeaders(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{})

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Headers", "X-Good, Bad Header, Content-Type")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "X-Good, Content-Type" {
		t.Errorf("Access-Control-Allow-Headers=%q want the illegal name dropped", got)
	}
}

// TestServer_CORSPreflightAllIllegalFallsBackToDefaults covers an echo that
// sanitizes down to nothing: the defaults are sent rather than an empty header.
func TestServer_CORSPreflightAllIllegalFallsBackToDefaults(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{})

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Headers", "Bad@Header")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization, Accept, X-Requested-With" {
		t.Errorf("Access-Control-Allow-Headers=%q want the defaults", got)
	}
}

// TestServer_CORSPreflightWithoutOrigin covers a non-browser OPTIONS probe: it
// still gets 204 so behaviour is unchanged, but no CORS headers.
func TestServer_CORSPreflightWithoutOrigin(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{})

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
	assertNoCORSHeaders(t, w, "OPTIONS without Origin")
}

// TestServer_CORSPreflightBypassesAuth guards the middleware ordering: browsers
// never attach credentials to a preflight, so a 401 there breaks every
// cross-origin request to an API-key protected instance.
func TestServer_CORSPreflightBypassesAuth(t *testing.T) {
	s := newTestServerWithConfig(
		config.Config{RequiredAPIKeys: []string{"sk-test"}},
		newStubRouter([]string{"m1"}, "OK"),
		newStubRouter(nil, ""),
	)

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204, preflight must not require an API key", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin=%q want *", got)
	}
}

func TestServer_CORSAllowedOriginsExactMatch(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{
		AllowedOrigins: []string{"https://dash.example.com"},
	})

	req := httptest.NewRequest(http.MethodGet, "/running", nil)
	req.Header.Set("Origin", "https://dash.example.com")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://dash.example.com" {
		t.Errorf("Access-Control-Allow-Origin=%q want the origin echoed, never *", got)
	}
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary=%q want it to contain Origin", got)
	}
}

// TestServer_CORSAllowedOriginsMismatch checks a disallowed origin gets no CORS
// headers while the request itself is still served normally.
func TestServer_CORSAllowedOriginsMismatch(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{
		AllowedOrigins: []string{"https://dash.example.com"},
	})

	req := httptest.NewRequest(http.MethodGet, "/running", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200, CORS must not change the response body path", w.Code)
	}
	assertNoCORSHeaders(t, w, "disallowed origin")
}

func TestServer_CORSAllowCredentials(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{
		AllowedOrigins:   []string{"https://dash.example.com"},
		AllowCredentials: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/running", nil)
	req.Header.Set("Origin", "https://dash.example.com")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials=%q want true", got)
	}
	// "*" with credentials is rejected at config load; make sure the runtime
	// never produces that pairing either.
	if got := w.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Error("Access-Control-Allow-Origin=* must never be sent with credentials")
	}
}

func TestServer_CORSExposeHeaders(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{
		AllowedOrigins: []string{"*"},
		ExposedHeaders: []string{"X-Request-Id", "X-Model"},
	})

	req := httptest.NewRequest(http.MethodGet, "/running", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Expose-Headers"); got != "X-Request-Id, X-Model" {
		t.Errorf("Access-Control-Expose-Headers=%q", got)
	}
}

// TestServer_CORSExposeHeadersOmittedWhenEmpty keeps an unset list from sending
// an empty header, which is the shape that folds badly downstream.
func TestServer_CORSExposeHeadersOmittedWhenEmpty(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{})

	req := httptest.NewRequest(http.MethodGet, "/running", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if _, ok := w.Header()["Access-Control-Expose-Headers"]; ok {
		t.Error("Access-Control-Expose-Headers must be omitted, not sent empty")
	}
}

func TestServer_CORSCustomMethodsAndMaxAge(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Content-Type"},
		MaxAge:         60,
	})

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "http://example.com")
	// A configured allowedHeaders list wins over the request's echo.
	req.Header.Set("Access-Control-Request-Headers", "X-Custom")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	want := map[string]string{
		"Access-Control-Allow-Methods": "GET, POST",
		"Access-Control-Allow-Headers": "Content-Type",
		"Access-Control-Max-Age":       "60",
	}
	for name, value := range want {
		if got := w.Header().Get(name); got != value {
			t.Errorf("%s=%q want %q", name, got, value)
		}
	}
}

// TestServer_CORSDefaultPolicyMatchesLegacy pins the behaviour an absent
// security block must keep, so adding the config never silently changes an
// existing deployment.
func TestServer_CORSDefaultPolicyMatchesLegacy(t *testing.T) {
	// A zero config.Config, i.e. no security block at all.
	s := newTestServer(newStubRouter([]string{"m1"}, "OK"), newStubRouter(nil, ""))

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
	want := map[string]string{
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		"Access-Control-Allow-Headers": "Content-Type, Authorization, Accept, X-Requested-With",
		"Access-Control-Max-Age":       "86400",
	}
	for name, value := range want {
		if got := w.Header().Get(name); got != value {
			t.Errorf("%s=%q want %q", name, got, value)
		}
	}
}

// TestServer_CORSDeclaredBlockKeepsPreflightDefaults is the runtime half of
// the two-mode model. Declaring security.cors takes charge of which origins
// may reach llama-swap, but not of preflight mechanics: a block that names
// only origins must still answer a browser's preflight with usable methods,
// headers and max-age, or a cross-origin POST would be blocked by a config
// that looks complete.
func TestServer_CORSDeclaredBlockKeepsPreflightDefaults(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{
		AllowedOrigins: []string{"https://dash.example.com"},
	})

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://dash.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
	want := map[string]string{
		"Access-Control-Allow-Origin":  "https://dash.example.com",
		"Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		"Access-Control-Allow-Headers": "Content-Type, Authorization, Accept, X-Requested-With",
		"Access-Control-Max-Age":       "86400",
	}
	for name, value := range want {
		if got := w.Header().Get(name); got != value {
			t.Errorf("%s=%q want %q", name, got, value)
		}
	}
}

// TestServer_CORSDeclaredBlockDoesNotWidenToWildcard guards the point of the
// two-mode model: a declared block never falls back to allowing any origin.
func TestServer_CORSDeclaredBlockDoesNotWidenToWildcard(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{
		AllowedOrigins: []string{"https://dash.example.com"},
	})

	for _, origin := range []string{"https://evil.example.com", "http://localhost:5173", "null"} {
		req := httptest.NewRequest(http.MethodGet, "/running", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		assertNoCORSHeaders(t, w, "unlisted origin "+origin)
	}
}

// pnaTestConfig is a policy with Private Network Access turned on, which
// requires explicit origins.
func pnaTestConfig() config.CORSConfig {
	return config.CORSConfig{
		AllowedOrigins:      []string{"https://dash.example.com"},
		AllowPrivateNetwork: true,
	}
}

// pnaPreflight builds a Chrome-style Private Network Access preflight.
func pnaPreflight(origin string, ask bool) *http.Request {
	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	if ask {
		req.Header.Set("Access-Control-Request-Private-Network", "true")
	}
	return req
}

func TestServer_CORSPrivateNetworkPreflight(t *testing.T) {
	s := corsTestServer(t, pnaTestConfig())

	w := httptest.NewRecorder()
	s.ServeHTTP(w, pnaPreflight("https://dash.example.com", true))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Errorf("Access-Control-Allow-Private-Network=%q want true", got)
	}
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Access-Control-Request-Private-Network") {
		t.Errorf("Vary=%q want it to contain Access-Control-Request-Private-Network", got)
	}
}

// TestServer_CORSPrivateNetworkOnlyWhenRequested keeps the header off an
// ordinary preflight, so it is never sent to a browser that did not ask.
func TestServer_CORSPrivateNetworkOnlyWhenRequested(t *testing.T) {
	s := corsTestServer(t, pnaTestConfig())

	w := httptest.NewRecorder()
	s.ServeHTTP(w, pnaPreflight("https://dash.example.com", false))

	if _, ok := w.Header()["Access-Control-Allow-Private-Network"]; ok {
		t.Error("Access-Control-Allow-Private-Network sent on a preflight that did not request it")
	}
}

// TestServer_CORSPrivateNetworkOffByDefault is the backwards-compatibility
// guard: llama-swap never sent this header, so a config that does not ask for
// it must not start, even when a browser requests it.
func TestServer_CORSPrivateNetworkOffByDefault(t *testing.T) {
	configs := map[string]config.CORSConfig{
		"legacy permissive policy": {},
		"origins but no opt-in":    {AllowedOrigins: []string{"https://dash.example.com"}},
	}

	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			s := corsTestServer(t, cfg)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, pnaPreflight("https://dash.example.com", true))

			if _, ok := w.Header()["Access-Control-Allow-Private-Network"]; ok {
				t.Error("Access-Control-Allow-Private-Network sent without being configured")
			}
		})
	}
}

// TestServer_CORSPrivateNetworkNotOnActualResponse covers the header being
// preflight-only: it has no meaning on a real response.
func TestServer_CORSPrivateNetworkNotOnActualResponse(t *testing.T) {
	s := corsTestServer(t, pnaTestConfig())

	req := httptest.NewRequest(http.MethodGet, "/running", nil)
	req.Header.Set("Origin", "https://dash.example.com")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if _, ok := w.Header()["Access-Control-Allow-Private-Network"]; ok {
		t.Error("Access-Control-Allow-Private-Network sent on a non-preflight response")
	}
}

func TestServer_CORSPrivateNetworkDisallowedOrigin(t *testing.T) {
	s := corsTestServer(t, pnaTestConfig())

	w := httptest.NewRecorder()
	s.ServeHTTP(w, pnaPreflight("https://evil.example.com", true))

	assertNoCORSHeaders(t, w, "PNA preflight from an unlisted origin")
}

// TestServer_CORSPrivateNetworkNeverWithWildcardOrigin pins the invariant that
// private-network access is never granted alongside a wildcard origin, whether
// the wildcard came from the legacy fallback or was written out. Config
// validation rejects both of these, so this exercises newCORSPolicy directly:
// the guarantee has to survive without the validator in front of it.
func TestServer_CORSPrivateNetworkNeverWithWildcardOrigin(t *testing.T) {
	configs := map[string]config.CORSConfig{
		"legacy fallback to wildcard": {AllowPrivateNetwork: true},
		"explicit wildcard": {
			AllowedOrigins:      []string{"*"},
			AllowPrivateNetwork: true,
		},
	}

	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			if newCORSPolicy(cfg).allowPrivateNetwork {
				t.Error("allowPrivateNetwork survived alongside a wildcard origin")
			}
		})
	}
}

// TestServer_CORSOriginMatchIsHostCaseInsensitive covers a config that spells
// the host in different case than the browser does. The match must ignore
// case, and the answer must be the request's spelling: a browser compares
// Access-Control-Allow-Origin against its own origin, which it serializes with
// the host lowercased, so replying with the config's mixed-case spelling would
// fail that check on an origin that is in fact allowed.
func TestServer_CORSOriginMatchIsHostCaseInsensitive(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{
		AllowedOrigins: []string{"https://Dash.Example.com"},
	})

	const origin = "https://dash.example.com"
	req := httptest.NewRequest(http.MethodGet, "/running", nil)
	req.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("Access-Control-Allow-Origin=%q want %q, the origin the request sent", got, origin)
	}
}

// TestServer_CORSNullOriginNotAllowedByList checks that the sandboxed-document
// "null" origin does not match a configured list.
func TestServer_CORSNullOriginNotAllowedByList(t *testing.T) {
	s := corsTestServer(t, config.CORSConfig{
		AllowedOrigins: []string{"https://dash.example.com"},
	})

	req := httptest.NewRequest(http.MethodGet, "/running", nil)
	req.Header.Set("Origin", "null")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	assertNoCORSHeaders(t, w, `Origin: null`)
}

func TestServer_SanitizeAccessControlRequestHeaders(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Content-Type, Authorization", "Content-Type, Authorization"},
		{"  X-Custom ,  Accept ", "X-Custom, Accept"},
		{"Valid, Bad Header", "Valid"},
		{"Bad@Header", ""},
		{"", ""},
		{",,,", ""},
		{"X-A,,X-B", "X-A, X-B"},
		{"X-A,", "X-A"},
		{"\tX-Tabbed\t", "X-Tabbed"},
	}
	for _, c := range cases {
		if got := sanitizeAccessControlRequestHeaderValues(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
