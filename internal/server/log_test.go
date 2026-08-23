package server

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

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

// TestServer_RequestLogMiddleware_ClientClosed covers #1029: a client that
// hangs up before anything is written (e.g. during a cold model load) used to
// be logged as the seeded 200, hiding aborted requests from status-code
// monitoring. It must be logged as 499 instead — but only when no response had
// started.
func TestServer_RequestLogMiddleware_ClientClosed(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string
		notWant string
	}{
		{
			// The cold-load cancellation branches in baseRouter.ServeHTTP:
			// they return without touching the ResponseWriter.
			name:    "no response written is logged as 499",
			handler: func(w http.ResponseWriter, r *http.Request) {},
			want:    "499 0",
			notWant: "200 0",
		},
		{
			// A response already on the wire keeps the status the client
			// actually received, even though it hung up mid-stream.
			name: "response already started keeps its status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("partial"))
			},
			want:    "200 7",
			notWant: "499",
		},
		{
			// An implicit 200 from a bare Write counts as started too.
			name: "implicit 200 from Write keeps its status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("partial"))
			},
			want:    "200 7",
			notWant: "499",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proxylog := logmon.NewWriter(io.Discard)
			ctx, cancel := context.WithCancel(context.Background())
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			r.RemoteAddr = "192.168.1.1:5000"
			// The client goes away while the handler is still waiting.
			cancel()

			CreateRequestLogMiddleware(proxylog)(c.handler).ServeHTTP(httptest.NewRecorder(), r)

			line := string(proxylog.GetHistory())
			if !strings.Contains(line, c.want) {
				t.Errorf("log line %q missing %q", line, c.want)
			}
			if strings.Contains(line, c.notWant) {
				t.Errorf("log line %q should not contain %q", line, c.notWant)
			}
		})
	}

	// A streaming handler can commit the implicit 200 by flushing alone. The
	// client has started receiving that response, so a later disconnect must
	// not rewrite the log line to 499.
	t.Run("cancellation after Flush keeps the flushed status", func(t *testing.T) {
		proxylog := logmon.NewWriter(io.Discard)
		ctx, cancel := context.WithCancel(context.Background())
		r := httptest.NewRequest(http.MethodGet, "/logs/stream", nil).WithContext(ctx)
		r.RemoteAddr = "192.168.1.1:5000"

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Establish the stream, then the client goes away mid-stream.
			w.(http.Flusher).Flush()
			cancel()
		})
		CreateRequestLogMiddleware(proxylog)(handler).ServeHTTP(httptest.NewRecorder(), r)

		line := string(proxylog.GetHistory())
		if !strings.Contains(line, "200 0") {
			t.Errorf("log line %q should keep the flushed 200", line)
		}
		if strings.Contains(line, "499") {
			t.Errorf("log line %q must not report 499 after a flushed response", line)
		}
	})

	// base.go calls SendError after the loading stream has already sent its
	// 200 (shutdown, or a dispatch error). net/http drops that second header,
	// so the client keeps the 200 and the log must report what it received.
	t.Run("WriteHeader after a started response keeps the first status", func(t *testing.T) {
		proxylog := logmon.NewWriter(io.Discard)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		r.RemoteAddr = "192.168.1.1:5000"

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("data: loading\n\n"))
			// Something fails after the stream has started.
			w.WriteHeader(http.StatusInternalServerError)
		})

		rec := httptest.NewRecorder()
		CreateRequestLogMiddleware(proxylog)(handler).ServeHTTP(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("client received %d, want %d", rec.Code, http.StatusOK)
		}
		if line := string(proxylog.GetHistory()); !strings.Contains(line, "200 15") {
			t.Errorf("log line %q should report the 200 the client received", line)
		}
	})

	t.Run("live client keeps the seeded 200", func(t *testing.T) {
		proxylog := logmon.NewWriter(io.Discard)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		r.RemoteAddr = "192.168.1.1:5000"
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

		CreateRequestLogMiddleware(proxylog)(handler).ServeHTTP(httptest.NewRecorder(), r)

		if line := string(proxylog.GetHistory()); !strings.Contains(line, "200 0") {
			t.Errorf("log line %q should report 200 for a connected client", line)
		}
	})
}

// TestServer_RequestLogMiddleware_WebSocketUpgrade verifies that the access-log
// middleware (which wraps responses in statusRecorder) does not break websocket
// upgrades proxied through httputil.ReverseProxy. ReverseProxy requires the
// ResponseWriter to implement http.Hijacker to take over the connection; if
// statusRecorder does not forward Hijack, the upgrade is refused with 502.
func TestServer_RequestLogMiddleware_WebSocketUpgrade(t *testing.T) {
	// Upstream: complete the upgrade handshake then echo bytes back. This
	// stands in for an upstream that speaks websocket; ReverseProxy only cares
	// about the 101 response and then copies raw bytes both ways.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("upstream ResponseWriter is not an http.Hijacker")
			return
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			t.Errorf("upstream hijack: %v", err)
			return
		}
		defer conn.Close()
		brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		brw.Flush()
		// Echo whatever the client sends.
		buf := make([]byte, 64)
		n, err := brw.Read(buf)
		if err != nil {
			return
		}
		brw.Write(buf[:n])
		brw.Flush()
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}

	// Front server: ReverseProxy wrapped in the access-log middleware, which is
	// the production statusRecorder-wrapped path.
	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	mw := CreateRequestLogMiddleware(logmon.NewWriter(io.Discard))
	front := httptest.NewServer(mw(proxy))
	defer front.Close()

	frontURL, err := url.Parse(front.URL)
	if err != nil {
		t.Fatalf("parse front URL: %v", err)
	}

	conn, err := net.DialTimeout("tcp", frontURL.Host, 5*time.Second)
	if err != nil {
		t.Fatalf("dial front: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "GET / HTTP/1.1\r\n" +
		"Host: " + frontURL.Host + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write upgrade request: %v", err)
	}

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	if !strings.Contains(statusLine, "101") {
		t.Fatalf("websocket upgrade failed: status line = %q, want 101 Switching Protocols", strings.TrimSpace(statusLine))
	}

	// Drain the rest of the response headers.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read headers: %v", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}

	// Verify bytes flow through the hijacked connection.
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(br, echo); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(echo) != "ping" {
		t.Errorf("echo = %q, want %q", echo, "ping")
	}
}
