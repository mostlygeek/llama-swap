package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWolProxy_SendMagicPacketValidation(t *testing.T) {
	err := sendMagicPacket("invalid-mac", "")
	if err == nil {
		t.Fatal("expected error for invalid MAC address")
	}

	err = sendMagicPacket("AA:BB:CC:DD:EE:FF", "")
	_ = err

	err = sendMagicPacket("AA:BB:CC:DD:EE:FF", "127.0.0.1")
	_ = err
}

func TestWolProxy_SendMagicPacketShortMAC(t *testing.T) {
	err := sendMagicPacket("AA:BB:CC", "")
	if err == nil {
		t.Fatal("expected error for short MAC address")
	}
}

func TestWolProxy_IsClientDisconnect(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: true,
		},
		{
			name:     "wrapped context canceled",
			err:      fmt.Errorf("request failed: %w", context.Canceled),
			expected: true,
		},
		{
			name:     "broken pipe",
			err:      errors.New("write: broken pipe"),
			expected: true,
		},
		{
			name:     "connection reset by peer",
			err:      errors.New("read: connection reset by peer"),
			expected: true,
		},
		{
			name:     "connection refused",
			err:      errors.New("dial tcp: connection refused"),
			expected: false,
		},
		{
			name:     "timeout",
			err:      errors.New("dial tcp: i/o timeout"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isClientDisconnect(tt.err)
			if got != tt.expected {
				t.Errorf("isClientDisconnect(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestWolProxy_ErrorHandlerUpstreamFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstreamAddr := "http://" + listener.Addr().String()
	listener.Close()

	p := &proxyServer{
		status:    ready,
		failCount: 0,
	}

	t.Run("api path does not write", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)

		upstreamErr := fmt.Errorf("dial tcp %s: connection refused", upstreamAddr)

		errorHandler := func(w http.ResponseWriter, r *http.Request, err error) {
			if isClientDisconnect(err) {
				return
			}
			p.setStatus(notready)
			p.incFail(1)

			path := r.URL.Path
			if path == "/" || strings.HasPrefix(path, "/ui/") {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, loadingPageHTML)
			}
		}

		pw := &proxyResponseWriter{ResponseWriter: w, written: false}
		errorHandler(pw, r, upstreamErr)

		if pw.written {
			t.Error("expected ErrorHandler to not write for API path")
		}
		if p.getStatus() != notready {
			t.Error("expected status to be downgraded to notready")
		}
		if p.getFailures() != 1 {
			t.Errorf("expected failCount 1, got %d", p.getFailures())
		}
	})

	t.Run("browser path returns loading page", func(t *testing.T) {
		p.setStatus(ready)
		p.resetFailures()

		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)

		upstreamErr := fmt.Errorf("dial tcp: connection refused")

		errorHandler := func(w http.ResponseWriter, r *http.Request, err error) {
			if isClientDisconnect(err) {
				return
			}
			p.setStatus(notready)
			p.incFail(1)

			path := r.URL.Path
			if path == "/" || strings.HasPrefix(path, "/ui/") {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, loadingPageHTML)
			}
		}

		pw := &proxyResponseWriter{ResponseWriter: w, written: false}
		errorHandler(pw, r, upstreamErr)

		if !pw.written {
			t.Error("expected ErrorHandler to write for browser path")
		}
		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
			t.Errorf("expected text/html content type, got %q", w.Header().Get("Content-Type"))
		}
		if p.getStatus() != notready {
			t.Error("expected status to be downgraded to notready")
		}
	})

	t.Run("client disconnect does not downgrade", func(t *testing.T) {
		p.setStatus(ready)
		p.resetFailures()

		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/models", nil)

		errorHandler := func(w http.ResponseWriter, r *http.Request, err error) {
			if isClientDisconnect(err) {
				return
			}
			p.setStatus(notready)
			p.incFail(1)
		}

		errorHandler(w, r, context.Canceled)

		if p.getStatus() != ready {
			t.Error("expected status to remain ready for client disconnect")
		}
		if p.getFailures() != 0 {
			t.Errorf("expected failCount 0, got %d", p.getFailures())
		}
	})
}

func TestWolProxy_StatusEndpoint(t *testing.T) {
	p := &proxyServer{
		status:    ready,
		failCount: 3,
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/status", nil)

	p.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "status: ready") {
		t.Errorf("expected 'status: ready' in body, got %q", body)
	}
	if !strings.Contains(body, "failures: 3") {
		t.Errorf("expected 'failures: 3' in body, got %q", body)
	}
}

func TestWolProxy_ServeHTTPNotReady(t *testing.T) {
	p := &proxyServer{
		status:    notready,
		failCount: 0,
	}

	t.Run("browser gets loading page", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		p.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), "html") {
			t.Error("expected HTML loading page")
		}
	})

	t.Run("SSE events returns 204", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/events", nil)
		p.ServeHTTP(w, r)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected 204, got %d", w.Code)
		}
	})

	t.Run("api path polls and times out", func(t *testing.T) {
		origTimeout := *flagTimeout
		*flagTimeout = 1
		defer func() { *flagTimeout = origTimeout }()

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		p.ServeHTTP(w, r)

		if w.Code != http.StatusRequestTimeout {
			t.Errorf("expected 408, got %d", w.Code)
		}
	})
}

func TestWolProxy_FullErrorHandler(t *testing.T) {
	origTimeout := *flagTimeout
	*flagTimeout = 1
	defer func() { *flagTimeout = origTimeout }()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()

	backend.Close()

	backendURL, _ := http.NewRequest("GET", backend.URL, nil)
	proxy := newProxy(backendURL.URL)

	proxy.setStatus(ready)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)

	proxy.ServeHTTP(w, r)

	if w.Code != http.StatusRequestTimeout {
		t.Errorf("expected 408 (retry timed out), got %d", w.Code)
	}
	if proxy.getStatus() != notready {
		t.Error("expected status downgrade to notready")
	}
}

func TestWolProxy_SSEReconnectsAfterUpstreamFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	server := &http.Server{Handler: mux}
	go server.Serve(listener)

	upstreamURL, _ := url.Parse("http://" + addr)
	proxy := newProxy(upstreamURL)

	waitForStatus := func(target upstreamStatus, timeout time.Duration) bool {
		deadline := time.After(timeout)
		for proxy.getStatus() != target {
			select {
			case <-deadline:
				return false
			case <-time.After(100 * time.Millisecond):
			}
		}
		return true
	}

	if !waitForStatus(ready, 10*time.Second) {
		t.Fatal("proxy never reached ready")
	}

	server.Close()

	type result struct {
		code int
		body string
	}
	resultCh := make(chan result, 1)

	go func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"prompt":"test"}`))
		proxy.ServeHTTP(w, r)
		resultCh <- result{code: w.Code, body: w.Body.String()}
	}()

	time.Sleep(500 * time.Millisecond)
	if proxy.getStatus() != notready {
		t.Error("expected not ready after upstream failure")
	}

	listener2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to re-listen on %s: %v", addr, err)
	}
	server2 := &http.Server{Handler: mux}
	go server2.Serve(listener2)
	defer server2.Close()

	select {
	case res := <-resultCh:
		if res.code != http.StatusOK {
			t.Errorf("expected 200 after retry, got %d", res.code)
		}
		if !strings.Contains(res.body, `"ok":true`) {
			t.Errorf("expected upstream response body, got %q", res.body)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("request never completed after upstream restart")
	}
}
