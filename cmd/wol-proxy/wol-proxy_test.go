package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newTestProxy returns a proxy whose upstream is unreachable, so it stays "not ready"
func newTestProxy(t *testing.T, requireConfirm bool) *proxyServer {
	t.Helper()
	oldConfirm, oldMac := *flagRequireConfirm, *flagMac
	*flagRequireConfirm = requireConfirm
	*flagMac = "" // sending the magic packet fails harmlessly
	t.Cleanup(func() { *flagRequireConfirm, *flagMac = oldConfirm, oldMac })

	u, _ := url.Parse("http://127.0.0.1:1")
	return newProxy(u, "")
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

func TestProxy_RequireConfirmWakeShowsLoadingPage(t *testing.T) {
	p := newTestProxy(t, true)
	rec := serve(p, "POST", wakePath)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Waking up upstream server") {
		t.Fatalf("expected loading page, got %q", rec.Body.String())
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
