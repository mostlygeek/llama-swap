package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

func newProcessCommand(t *testing.T, conf config.ModelConfig) *ProcessCommand {
	t.Helper()
	logger := logmon.NewWriter(io.Discard)
	p, err := New(context.Background(), t.Name(), conf, logger, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestProcessCommand_StartStop(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent", "-respond hello")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})
	t.Cleanup(func() { p.Stop(0, 5*time.Second) })

	req := httptest.NewRequest("GET", "/test", nil)

	// before start: no handler
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("before start: expected 500, got %d", rr.Code)
	}

	if err := p.Run(10 * time.Second); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if got := p.State(); got != StateReady {
		t.Errorf("after Run: expected state %s, got %s", StateReady, got)
	}

	rr = httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("after Run: expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "hello" {
		t.Errorf("expected body %q, got %q", "hello", body)
	}

	if err := p.Stop(0, 5*time.Second); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if got := p.State(); got != StateStopped {
		t.Errorf("after Stop: expected state %s, got %s", StateStopped, got)
	}

	// after stop: handler cleared
	rr = httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("after stop: expected 500, got %d", rr.Code)
	}
}

func TestProcessCommand_Run_Idempotent(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})
	t.Cleanup(func() { p.Stop(0, 5*time.Second) })

	if err := p.Run(10 * time.Second); err != nil {
		t.Fatalf("first Run() error: %v", err)
	}

	if err := p.Run(10 * time.Second); err == nil {
		t.Error("second Run() while running: expected error, got nil")
	}
}

func TestProcessCommand_Stop_Idempotent(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})

	if err := p.Stop(0, 5*time.Second); err != nil {
		t.Fatalf("Stop() before Run(): %v", err)
	}

	if err := p.Run(10 * time.Second); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if err := p.Stop(0, 5*time.Second); err != nil {
		t.Fatalf("first Stop() error: %v", err)
	}

	if err := p.Stop(0, 5*time.Second); err != nil {
		t.Fatalf("second Stop() error: %v", err)
	}
}

// TestProcessCommand_StopCancelsRun verifies that a Stop sent while Run is
// executing its health-check loop returns ErrAbort to the Run caller.
//
// A blocking mock HTTP server is used as the proxy so the test can deterministically
// know when doStart is inside the health-check loop before issuing Stop.
func TestProcessCommand_StopCancelsRun(t *testing.T) {
	skipIfNoSimpleResponder(t)

	healthCheckStarted := make(chan struct{}, 1)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Signal that a health check is in-flight, then block until the client
		// cancels (which happens when Stop cancels the start context).
		select {
		case healthCheckStarted <- struct{}{}:
		default:
		}
		<-r.Context().Done()
		http.Error(w, "mock cancelled", http.StatusServiceUnavailable)
	}))
	defer mock.Close()

	// simple-responder is the real process; health checks go to the blocking mock.
	cmd, _ := simpleResponderCmd(t, "-silent")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              mock.URL,
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 30,
	})

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- p.Run(30 * time.Second)
	}()

	// Block until doStart is actually performing a health check, guaranteeing
	// that Run is in-flight when Stop is called.
	<-healthCheckStarted

	if err := p.Stop(0, 5*time.Second); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if err := <-runErrCh; !errors.Is(err, ErrStartAborted) {
		t.Errorf("expected ErrStartAborted from Run, got %v", err)
	}
}

// TestProcessCommand_RunStopCycle runs several sequential start/stop pairs on
// fresh processes to confirm they are reusable.
func TestProcessCommand_RunStopCycle(t *testing.T) {
	skipIfNoSimpleResponder(t)

	for i := range 3 {
		cmd, port := simpleResponderCmd(t, "-silent")
		p := newProcessCommand(t, config.ModelConfig{
			Cmd:                cmd,
			Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
			CheckEndpoint:      "/health",
			HealthCheckTimeout: 10,
		})

		if err := p.Run(10 * time.Second); err != nil {
			t.Fatalf("cycle %d Run() error: %v", i, err)
		}

		req := httptest.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()
		p.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("cycle %d: expected 200 from /health, got %d", i, rr.Code)
		}

		if err := p.Stop(0, 5*time.Second); err != nil {
			t.Fatalf("cycle %d Stop() error: %v", i, err)
		}
	}
}

// TestProcessCommand_ReverseProxyPanicIsRecovered drives the full proxy path:
// the upstream responds healthy on /health (so Run completes), then on the
// actual proxied request it hijacks the connection and closes it mid-body.
// That upstream EOF makes httputil.ReverseProxy.copyResponse return an error,
// which panics with http.ErrAbortHandler — the wrapped handlerFn must recover
// and log the disconnect.
//
// Requests are issued through an httptest.NewServer wrapping the process so
// the panic actually fires (httputil only panics on copy errors when the
// request carries http.ServerContextKey, which a real server sets).
//
// see: https://github.com/golang/go/issues/23643
func TestProcessCommand_ReverseProxyPanicIsRecovered(t *testing.T) {
	skipIfNoSimpleResponder(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Send a Content-Length that promises 100 bytes, deliver only a few,
		// then slam the connection shut. The reverse proxy will see EOF
		// before the body is fully copied and panic with ErrAbortHandler.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("upstream: hijack not supported")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("upstream: hijack: %v", err)
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\nContent-Type: text/plain\r\n\r\npartial"))
		_ = conn.Close()
	}))
	t.Cleanup(upstream.Close)

	// Capture proxy log output so we can assert the recover message was
	// emitted by handlerFn.
	logBuf := &syncBuffer{}
	proxyLogger := logmon.NewWriter(logBuf)
	procLogger := logmon.NewWriter(io.Discard)

	cmd, _ := simpleResponderCmd(t, "-silent")
	p, err := New(context.Background(), t.Name(), config.ModelConfig{
		Cmd:                cmd,
		Proxy:              upstream.URL,
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	}, procLogger, proxyLogger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { p.Stop(0, 5*time.Second) })

	if err := p.Run(10 * time.Second); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Wrap p in an httptest server so requests get http.ServerContextKey
	// automatically — that is what makes httputil.ReverseProxy raise the panic.
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	resp, err := http.Get(front.URL + "/disconnect")
	if err == nil {
		resp.Body.Close()
	}

	const want = "recovered from upstream disconnection"
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logBuf.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("expected proxy log to contain %q; got:\n%s", want, logBuf.String())
}

// syncBuffer is a concurrent-safe bytes.Buffer for capturing logmon output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestProcessCommand_ConcurrentRunStop launches many concurrent run/stop racing
// pairs to exercise the race detector and verify no deadlocks occur.
func TestProcessCommand_ConcurrentRunStop(t *testing.T) {
	skipIfNoSimpleResponder(t)

	var wg sync.WaitGroup
	for range 10 {
		cmd, port := simpleResponderCmd(t, "-silent")
		p := newProcessCommand(t, config.ModelConfig{
			Cmd:                cmd,
			Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
			CheckEndpoint:      "/health",
			HealthCheckTimeout: 10,
		})

		wg.Add(2)
		go func() {
			defer wg.Done()
			p.Run(10 * time.Second) //nolint: errcheck — one goroutine wins the race
		}()
		go func() {
			defer wg.Done()
			p.Stop(0, 5*time.Second) //nolint: errcheck
		}()
		wg.Wait()

		// ensure clean state regardless of race outcome
		p.Stop(0, 5*time.Second) //nolint: errcheck
	}
}
