package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProcess_AutomaticallyStartsUpstream(t *testing.T) {
	logMonitor := NewLogMonitorWriter(io.Discard)
	expectedMessage := "testing91931"
	config := getTestSimpleResponderConfig(expectedMessage)

	// Create a process
	process := NewProcess("test-process", 5, config, logMonitor)
	defer process.Stop()

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// process is automatically started
	assert.Equal(t, StateStopped, process.CurrentState())
	process.ProxyRequest(w, req)
	assert.Equal(t, StateReady, process.CurrentState())

	assert.Equal(t, http.StatusOK, w.Code, "Expected status code %d, got %d", http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), expectedMessage)

	// Stop the process
	process.Stop()

	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()

	// Proxy the request
	process.ProxyRequest(w, req)

	// should have automatically started the process again
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}
}

// test that the automatic start returns the expected error type
func TestProcess_BrokenModelConfig(t *testing.T) {
	// Create a process configuration
	config := ModelConfig{
		Cmd:           "nonexistent-command",
		Proxy:         "http://127.0.0.1:9913",
		CheckEndpoint: "/health",
	}

	process := NewProcess("broken", 1, config, NewLogMonitor())
	defer process.Stop()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	process.ProxyRequest(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "unable to start process")
}

// test that the process unloads after the TTL
func TestProcess_UnloadAfterTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long auto unload TTL test")
	}

	expectedMessage := "I_sense_imminent_danger"
	config := getTestSimpleResponderConfig(expectedMessage)
	assert.Equal(t, 0, config.UnloadAfter)
	config.UnloadAfter = 3 // seconds
	assert.Equal(t, 3, config.UnloadAfter)

	process := NewProcess("ttl", 2, config, NewLogMonitorWriter(io.Discard))
	defer process.Stop()

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Proxy the request (auto start)
	process.ProxyRequest(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "Expected status code %d, got %d", http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), expectedMessage)

	assert.Equal(t, StateReady, process.CurrentState())

	// wait 5 seconds
	time.Sleep(5 * time.Second)
	assert.Equal(t, StateStopped, process.CurrentState())
}

// issue #19
func TestProcess_HTTPRequestsHaveTimeToFinish(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long auto unload TTL test")
	}

	expectedMessage := "12345"
	config := getTestSimpleResponderConfig(expectedMessage)
	process := NewProcess("t", 10, config, NewLogMonitorWriter(os.Stdout))
	defer process.Stop()

	results := map[string]string{
		"12345": "",
		"abcde": "",
		"fghij": "",
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for key := range results {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			// send a request that should take 5 * 200ms (1 second) to complete
			req := httptest.NewRequest("GET", fmt.Sprintf("/slow-respond?echo=%s&delay=200ms", key), nil)
			w := httptest.NewRecorder()

			process.ProxyRequest(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status OK, got %d for key %s", w.Code, key)
			}

			mu.Lock()
			results[key] = w.Body.String()
			mu.Unlock()

		}(key)
	}

	// stop the requests in the middle
	go func() {
		<-time.After(500 * time.Millisecond)
		process.Stop()
	}()

	wg.Wait()

	for key, result := range results {
		assert.Equal(t, key, result)
	}
}
