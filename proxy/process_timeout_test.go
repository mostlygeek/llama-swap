package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
)

// TestProcess_RequestTimeout verifies that requestTimeout actually kills the process
func TestProcess_RequestTimeout(t *testing.T) {
	// Create error channel to report handler errors from the mock server goroutine
	srvErrCh := make(chan error, 1)

	// Create a mock server that simulates a long-running inference
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Mock server received request")

		// Simulate streaming response that takes 60 seconds
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			srvErrCh <- fmt.Errorf("Expected http.ResponseWriter to be an http.Flusher")
			return
		}

		// Stream data for 60 seconds
		for i := 0; i < 60; i++ {
			select {
			case <-r.Context().Done():
				t.Logf("Mock server: client disconnected after %d seconds", i)
				return
			default:
				fmt.Fprintf(w, "data: token %d\n\n", i)
				flusher.Flush()
				time.Sleep(1 * time.Second)
			}
		}
		t.Logf("Mock server completed full 60 second response")
	}))
	defer mockServer.Close()

	// Setup process logger - use NewLogMonitor() to avoid race in test
	processLogger := NewLogMonitor()
	proxyLogger := NewLogMonitor()

	// Create process with 5 second request timeout
	cfg := config.ModelConfig{
		Proxy:          mockServer.URL,
		CheckEndpoint:  "none", // skip health check
		RequestTimeout: 5,      // 5 second timeout
	}

	p := NewProcess("test-timeout", 30, cfg, processLogger, proxyLogger)
	p.gracefulStopTimeout = 2 * time.Second // shorter for testing

	// Manually set state to ready (skip actual process start)
	p.forceState(StateReady)

	// Make a request that should timeout
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		p.ProxyRequest(w, req)
	}()

	// Wait for either completion or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case err := <-srvErrCh:
		// Handler error - fail the test immediately
		t.Fatalf("Mock server handler error: %v", err)

	case <-done:
		elapsed := time.Since(start)
		t.Logf("Request completed after %v", elapsed)

		// Check for any deferred server errors
		select {
		case err := <-srvErrCh:
			t.Fatalf("Mock server handler error: %v", err)
		default:
			// No server errors, continue with assertions
		}

		// Request should complete within timeout + gracefulStopTimeout + some buffer
		maxExpected := time.Duration(cfg.RequestTimeout+2)*time.Second + 3*time.Second
		if elapsed > maxExpected {
			t.Errorf("Request took %v, expected less than %v with 5s timeout", elapsed, maxExpected)
		} else {
			t.Logf("✓ Request was properly terminated by timeout")
		}

	case <-time.After(15 * time.Second):
		t.Fatalf("Test timed out after 15 seconds - request should have been killed by requestTimeout")
	}
}

// TestProcess_RequestTimeoutWithRealProcess tests with an actual process
func TestProcess_RequestTimeoutWithRealProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test with real process in short mode")
	}

	// This test would require a real llama.cpp server or similar
	// For now, we can skip it or mock it
	t.Skip("Requires real inference server")
}
