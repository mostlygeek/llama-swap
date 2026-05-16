package process

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

func skipIfNoSimpleResponder(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(simpleResponderPath); os.IsNotExist(err) {
		t.Skipf("simple-responder not found at %s, run `make simple-responder`", simpleResponderPath)
	}
}

func newCommandRuntime(t *testing.T, conf config.ModelConfig) Runtime {
	t.Helper()
	rt, err := NewCommandRuntime(conf, logmon.NewWriter(io.Discard))
	if err != nil {
		t.Fatalf("NewCommandRuntime: %v", err)
	}
	return rt
}

func TestCommandRuntime_StartStop(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent", "-respond hello")
	rt := newCommandRuntime(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})
	t.Cleanup(func() { rt.Stop(5 * time.Second) })

	req := httptest.NewRequest("GET", "/test", nil)

	// before start: no handler
	rr := httptest.NewRecorder()
	rt.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("before start: expected 500, got %d", rr.Code)
	}

	if err := rt.Start(10 * time.Second); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	rr = httptest.NewRecorder()
	rt.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("after start: expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "hello" {
		t.Errorf("expected body %q, got %q", "hello", body)
	}

	if err := rt.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	// after stop: handler cleared
	rr = httptest.NewRecorder()
	rt.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("after stop: expected 500, got %d", rr.Code)
	}
}

func TestCommandRuntime_Start_Idempotent(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent")
	rt := newCommandRuntime(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})
	t.Cleanup(func() { rt.Stop(5 * time.Second) })

	if err := rt.Start(10 * time.Second); err != nil {
		t.Fatalf("first Start() error: %v", err)
	}

	if err := rt.Start(10 * time.Second); err == nil {
		t.Error("second Start() while running: expected error, got nil")
	}
}

func TestCommandRuntime_Stop_Idempotent(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent")
	rt := newCommandRuntime(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})

	if err := rt.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop() before Start(): %v", err)
	}

	if err := rt.Start(10 * time.Second); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if err := rt.Stop(5 * time.Second); err != nil {
		t.Fatalf("first Stop() error: %v", err)
	}

	if err := rt.Stop(5 * time.Second); err != nil {
		t.Fatalf("second Stop() error: %v", err)
	}
}

// TestCommandRuntime_StopCancelsStart verifies that a Stop sent while Start is
// executing its health-check loop returns ErrAbort to the Start caller.
//
// A blocking mock HTTP server is used as the proxy so the test can deterministically
// know when doStart is inside the health-check loop before issuing Stop.
func TestCommandRuntime_StopCancelsStart(t *testing.T) {
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
		http.Error(w, "cancelled", http.StatusServiceUnavailable)
	}))
	defer mock.Close()

	// simple-responder is the real process; health checks go to the blocking mock.
	cmd, _ := simpleResponderCmd(t, "-silent")
	rt := newCommandRuntime(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              mock.URL,
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 30,
	})

	startErrCh := make(chan error, 1)
	go func() {
		startErrCh <- rt.Start(30 * time.Second)
	}()

	// Block until doStart is actually performing a health check, guaranteeing
	// that Start is in-flight when Stop is called.
	<-healthCheckStarted

	if err := rt.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if err := <-startErrCh; !errors.Is(err, ErrAbort) {
		t.Errorf("expected ErrAbort from Start, got %v", err)
	}
}

// TestCommandRuntime_StartStopCycle runs several sequential start/stop pairs
// on fresh runtimes to confirm they are reusable.
func TestCommandRuntime_StartStopCycle(t *testing.T) {
	skipIfNoSimpleResponder(t)

	for i := range 3 {
		cmd, port := simpleResponderCmd(t, "-silent")
		rt := newCommandRuntime(t, config.ModelConfig{
			Cmd:                cmd,
			Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
			CheckEndpoint:      "/health",
			HealthCheckTimeout: 10,
		})

		if err := rt.Start(10 * time.Second); err != nil {
			t.Fatalf("cycle %d Start() error: %v", i, err)
		}

		req := httptest.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()
		rt.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("cycle %d: expected 200 from /health, got %d", i, rr.Code)
		}

		if err := rt.Stop(5 * time.Second); err != nil {
			t.Fatalf("cycle %d Stop() error: %v", i, err)
		}
	}
}

// TestCommandRuntime_ConcurrentStartStop launches many concurrent start/stop
// racing pairs to exercise the race detector and verify no deadlocks occur.
func TestCommandRuntime_ConcurrentStartStop(t *testing.T) {
	skipIfNoSimpleResponder(t)

	var wg sync.WaitGroup
	for range 10 {
		cmd, port := simpleResponderCmd(t, "-silent")
		rt := newCommandRuntime(t, config.ModelConfig{
			Cmd:                cmd,
			Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
			CheckEndpoint:      "/health",
			HealthCheckTimeout: 10,
		})

		wg.Add(2)
		go func() {
			defer wg.Done()
			rt.Start(10 * time.Second) //nolint: errcheck — one goroutine wins the race
		}()
		go func() {
			defer wg.Done()
			rt.Stop(5 * time.Second) //nolint: errcheck
		}()
		wg.Wait()

		// ensure clean state regardless of race outcome
		rt.Stop(5 * time.Second) //nolint: errcheck
	}
}
