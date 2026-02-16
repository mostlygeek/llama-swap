package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func newTestProxyServer(t *testing.T, upstream http.Handler, status upstreamStatus) *proxyServer {
	t.Helper()
	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)

	upstreamURL, err := url.Parse(upstreamServer.URL)
	if err != nil {
		t.Fatalf("failed to parse upstream URL: %v", err)
	}

	return &proxyServer{
		upstreamProxy: httputil.NewSingleHostReverseProxy(upstreamURL),
		status:        status,
	}
}

func TestWolProxy_StatusEndpointReturnsStatusAndFailures(t *testing.T) {
	proxy := newTestProxyServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), ready)
	proxy.failCount = 3

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()

	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "status: ready") {
		t.Fatalf("expected status body to contain ready state, got %q", body)
	}
	if !strings.Contains(body, "failures: 3") {
		t.Fatalf("expected status body to contain failure count, got %q", body)
	}
}

func TestWolProxy_NotReadyRootReturnsLoadingPage(t *testing.T) {
	oldMac := *flagMac
	*flagMac = ""
	t.Cleanup(func() { *flagMac = oldMac })

	loadingPageHTML = "<html>loading</html>"
	proxy := newTestProxyServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be called when serving loading page")
	}), notready)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if got := rr.Body.String(); got != loadingPageHTML {
		t.Fatalf("expected loading page body %q, got %q", loadingPageHTML, got)
	}
}

func TestWolProxy_NotReadyAPIRequestTimesOut(t *testing.T) {
	oldMac := *flagMac
	oldTimeout := *flagTimeout
	*flagMac = ""
	*flagTimeout = 0
	t.Cleanup(func() {
		*flagMac = oldMac
		*flagTimeout = oldTimeout
	})

	proxy := newTestProxyServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be called when timeout is reached")
	}), notready)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rr := httptest.NewRecorder()

	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestTimeout {
		t.Fatalf("expected status 408, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "timeout") {
		t.Fatalf("expected timeout body, got %q", rr.Body.String())
	}
}

func TestWolProxy_ReadyStateProxiesRequests(t *testing.T) {
	proxy := newTestProxyServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("expected upstream path /v1/models, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("proxied"))
	}), ready)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()

	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", rr.Code)
	}

	body, err := io.ReadAll(rr.Result().Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != "proxied" {
		t.Fatalf("expected proxied body, got %q", string(body))
	}
}
