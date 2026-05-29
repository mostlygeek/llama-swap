package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
)

func TestServer_NewLoggers(t *testing.T) {
	t.Run("proxy mode routes proxy into muxlog, discards upstream", func(t *testing.T) {
		mux, proxy, upstream := NewLoggers(config.LogToStdoutProxy)
		proxy.Info("PROXYLINE")
		upstream.Info("UPSTREAMLINE")
		h := string(mux.GetHistory())
		if !strings.Contains(h, "PROXYLINE") {
			t.Errorf("muxlog missing proxy line: %q", h)
		}
		if strings.Contains(h, "UPSTREAMLINE") {
			t.Errorf("muxlog should not contain upstream line: %q", h)
		}
	})

	t.Run("both mode routes proxy and upstream into muxlog", func(t *testing.T) {
		mux, proxy, upstream := NewLoggers(config.LogToStdoutBoth)
		proxy.Info("PROXYLINE")
		upstream.Info("UPSTREAMLINE")
		h := string(mux.GetHistory())
		if !strings.Contains(h, "PROXYLINE") || !strings.Contains(h, "UPSTREAMLINE") {
			t.Errorf("muxlog history = %q", h)
		}
	})

	t.Run("none mode discards everything from muxlog", func(t *testing.T) {
		mux, proxy, upstream := NewLoggers(config.LogToStdoutNone)
		proxy.Info("PROXYLINE")
		upstream.Info("UPSTREAMLINE")
		if len(mux.GetHistory()) != 0 {
			t.Errorf("muxlog should be empty, got %q", mux.GetHistory())
		}
	})
}

func TestServer_HandleLogs_Plain(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.muxlog.Write([]byte("a log line"))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/logs", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if w.Body.String() != "a log line" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestServer_HandleLogs_HTMLRedirect(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/ui/" {
		t.Errorf("Location = %q, want /ui/", got)
	}
}

func TestServer_ClientIP(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*http.Request)
		want  string
	}{
		{"remote addr", func(r *http.Request) { r.RemoteAddr = "10.0.0.5:1234" }, "10.0.0.5"},
		{"x-forwarded-for", func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
		}, "1.2.3.4"},
		{"x-real-ip", func(r *http.Request) { r.Header.Set("X-Real-IP", "9.9.9.9") }, "9.9.9.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = ""
			c.setup(r)
			if got := clientIP(r); got != c.want {
				t.Errorf("clientIP() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestServer_RequestLogMiddleware(t *testing.T) {
	proxylog := logmon.NewWriter(io.Discard)
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("hello"))
	})
	mw := CreateRequestLogMiddleware(proxylog)

	t.Run("logs request", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		r.RemoteAddr = "192.168.1.1:5000"
		mw(final).ServeHTTP(httptest.NewRecorder(), r)

		line := string(proxylog.GetHistory())
		for _, want := range []string{"192.168.1.1", "POST /v1/chat/completions", "201", "5"} {
			if !strings.Contains(line, want) {
				t.Errorf("log line %q missing %q", line, want)
			}
		}
	})

	for _, path := range []string{"/wol-health", "/api/performance", "/metrics"} {
		t.Run("skips "+path, func(t *testing.T) {
			skipLog := logmon.NewWriter(io.Discard)
			skipMW := CreateRequestLogMiddleware(skipLog)
			skipMW(final).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
			if len(skipLog.GetHistory()) != 0 {
				t.Errorf("%s should not be logged; got %q", path, skipLog.GetHistory())
			}
		})
	}
}
