package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newTestProxy returns a proxy that starts "not ready" and has no background
// monitors, so tests fully control its status
func newTestProxy(t *testing.T, requireConfirm bool) *proxyServer {
	t.Helper()
	oldConfirm, oldMac := *flagRequireConfirm, *flagMac
	*flagRequireConfirm = requireConfirm
	*flagMac = "" // sending the magic packet fails harmlessly
	t.Cleanup(func() { *flagRequireConfirm, *flagMac = oldConfirm, oldMac })

	u, _ := url.Parse("http://127.0.0.1:1")
	return newProxyServer(u)
}

func serve(p *proxyServer, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestProxy_RequireConfirmRootShowsConfirmPage(t *testing.T) {
	p := newTestProxy(t, true)
	for _, path := range []string{"/", "/ui/"} {
		rec := serve(p, "GET", path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got status %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `action="/wol-wake"`) {
			t.Fatalf("%s: expected confirm page, got %q", path, rec.Body.String())
		}
	}
}

func TestProxy_RequireConfirmOtherRequestsReturnError(t *testing.T) {
	p := newTestProxy(t, true)
	rec := serve(p, "POST", "/v1/chat/completions")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestProxy_RequireConfirmWakeRedirectsToLoadingPage(t *testing.T) {
	p := newTestProxy(t, true)
	rec := serve(p, "POST", wakePath)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != wakePath {
		t.Fatalf("got status %d location %q, want redirect to %s", rec.Code, rec.Header().Get("Location"), wakePath)
	}

	// the redirect target is a GET page, so reloading it never re-submits the form
	rec = serve(p, "GET", wakePath)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Waking up upstream server") {
		t.Fatalf("expected loading page, got %q", rec.Body.String())
	}

	p.setStatus(ready)
	rec = serve(p, "GET", wakePath)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("ready: got status %d location %q, want redirect to /", rec.Code, rec.Header().Get("Location"))
	}
}

func TestProxy_RequireConfirmLoadingPageWithoutWakeGoesToRoot(t *testing.T) {
	p := newTestProxy(t, true)
	rec := serve(p, "GET", wakePath)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("got status %d location %q, want redirect to /", rec.Code, rec.Header().Get("Location"))
	}
}

func TestProxy_BeginWakeIgnoredWhenReady(t *testing.T) {
	p := newTestProxy(t, true)
	p.setStatus(ready)
	p.beginWake()
	if p.wakePending() {
		t.Fatal("a wake must not start while upstream is ready")
	}
}

func TestProxy_RequireConfirmWakeRedirectsWhenReady(t *testing.T) {
	p := newTestProxy(t, true)
	p.setStatus(ready)
	rec := serve(p, "POST", wakePath)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusSeeOther)
	}
}

func TestProxy_WakePathIgnoredWithoutRequireConfirm(t *testing.T) {
	p := newTestProxy(t, false)
	p.setStatus(ready)
	// proxied to the (dead) upstream instead of handled locally
	rec := serve(p, "POST", wakePath)
	if rec.Code == http.StatusSeeOther || rec.Code == http.StatusOK {
		t.Fatalf("wake endpoint should be disabled, got status %d", rec.Code)
	}
}

func TestProxy_LastWakeDurationIsRememberedAndShownInLoadingPage(t *testing.T) {
	p := newTestProxy(t, true)

	if got := p.loadingPage(); !strings.Contains(got, `data-expected="0"`) {
		t.Fatalf("expected no estimate before the first wake")
	}

	p.beginWake()
	p.statusMutex.Lock()
	p.wakeStart = time.Now().Add(-12500 * time.Millisecond) // pretend the wake began 12.5s ago
	p.statusMutex.Unlock()
	p.setStatus(ready)

	if d := p.lastWakeDuration(); d < 12*time.Second || d > 14*time.Second {
		t.Fatalf("unexpected last wake duration %v", d)
	}
	if got := p.loadingPage(); !strings.Contains(got, `data-expected="12.5"`) {
		t.Fatalf("expected the last wake duration in the loading page")
	}

	// becoming ready without a pending wake must not change the estimate
	p.setStatus(notready)
	p.setStatus(ready)
	if d := p.lastWakeDuration(); d < 12*time.Second || d > 14*time.Second {
		t.Fatalf("estimate changed without a wake: %v", d)
	}
}

func TestProxy_StaleWakeIsNotTracked(t *testing.T) {
	p := newTestProxy(t, true)
	p.statusMutex.Lock()
	p.wakeStart = time.Now().Add(-2 * maxTrackedWake)
	p.statusMutex.Unlock()
	p.setStatus(ready)
	if d := p.lastWakeDuration(); d != 0 {
		t.Fatalf("stale wake should be discarded, got %v", d)
	}
}
