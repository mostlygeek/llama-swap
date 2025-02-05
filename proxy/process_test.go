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

func TestProcess_UnloadAfterTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long auto unload TTL test")
	}

	expectedMessage := "I_sense_imminent_danger"
	config := getTestSimpleResponderConfig(expectedMessage)
	assert.Equal(t, 0, config.UnloadAfter)
	config.UnloadAfter = 3 // seconds
	assert.Equal(t, 3, config.UnloadAfter)

	process := NewProcess("ttl_test", 2, config, NewLogMonitorWriter(io.Discard))
	defer process.Stop()

	// this should take 4 seconds
	req1 := httptest.NewRequest("GET", "/slow-respond?echo=1234&delay=1000ms", nil)
	req2 := httptest.NewRequest("GET", "/test", nil)

	w := httptest.NewRecorder()

	// Proxy the request (auto start) with a slow response that takes longer than config.UnloadAfter
	process.ProxyRequest(w, req1)

	t.Log("sending slow first request (4 seconds)")
	assert.Equal(t, http.StatusOK, w.Code, "Expected status code %d, got %d", http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "1234")
	assert.Equal(t, StateReady, process.CurrentState())

	// ensure the TTL timeout does not race slow requests (see issue #25)
	t.Log("sending second request (1 second)")
	time.Sleep(time.Second)
	w = httptest.NewRecorder()
	process.ProxyRequest(w, req2)
	assert.Equal(t, http.StatusOK, w.Code, "Expected status code %d, got %d", http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), expectedMessage)
	assert.Equal(t, StateReady, process.CurrentState())

	// wait 5 seconds
	t.Log("sleep 5 seconds and check if unloaded")
	time.Sleep(5 * time.Second)
	assert.Equal(t, StateStopped, process.CurrentState())
}

func TestProcess_LowTTLValue(t *testing.T) {
	if true { // change this code to run this ...
		t.Skip("skipping test, edit process_test.go to run it ")
	}

	config := getTestSimpleResponderConfig("fast_ttl")
	assert.Equal(t, 0, config.UnloadAfter)
	config.UnloadAfter = 1 // second
	assert.Equal(t, 1, config.UnloadAfter)

	process := NewProcess("ttl", 2, config, NewLogMonitorWriter(os.Stdout))
	defer process.Stop()

	for i := 0; i < 100; i++ {
		t.Logf("Waiting before sending request %d", i)
		time.Sleep(1500 * time.Millisecond)

		expected := fmt.Sprintf("echo=test_%d", i)
		req := httptest.NewRequest("GET", fmt.Sprintf("/slow-respond?echo=%s&delay=50ms", expected), nil)
		w := httptest.NewRecorder()
		process.ProxyRequest(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), expected)
	}

}

// issue #19
func TestProcess_HTTPRequestsHaveTimeToFinish(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
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

func TestSetState(t *testing.T) {
	tests := []struct {
		name           string
		currentState   ProcessState
		newState       ProcessState
		expectedError  error
		expectedResult ProcessState
	}{
		{"Stopped to Starting", StateStopped, StateStarting, nil, StateStarting},
		{"Starting to Ready", StateStarting, StateReady, nil, StateReady},
		{"Starting to Failed", StateStarting, StateFailed, nil, StateFailed},
		{"Ready to Stopping", StateReady, StateStopping, nil, StateStopping},
		{"Stopping to Stopped", StateStopping, StateStopped, nil, StateStopped},
		{"Stopped to Ready", StateStopped, StateReady, fmt.Errorf("invalid state transition from stopped to ready"), StateStopped},
		{"Starting to Stopped", StateStarting, StateStopped, fmt.Errorf("invalid state transition from starting to stopped"), StateStarting},
		{"Ready to Starting", StateReady, StateStarting, fmt.Errorf("invalid state transition from ready to starting"), StateReady},
		{"Ready to Failed", StateReady, StateFailed, fmt.Errorf("invalid state transition from ready to failed"), StateReady},
		{"Stopping to Ready", StateStopping, StateReady, fmt.Errorf("invalid state transition from stopping to ready"), StateStopping},
		{"Failed to Stopped", StateFailed, StateStopped, fmt.Errorf("invalid state transition from failed to stopped"), StateFailed},
		{"Failed to Starting", StateFailed, StateStarting, fmt.Errorf("invalid state transition from failed to starting"), StateFailed},
		{"Shutdown to Stopped", StateShutdown, StateStopped, fmt.Errorf("invalid state transition from shutdown to stopped"), StateShutdown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &Process{
				state: test.currentState,
			}

			err := p.setState(test.newState)
			if err != nil && test.expectedError == nil {
				t.Errorf("Unexpected error: %v", err)
			} else if err == nil && test.expectedError != nil {
				t.Errorf("Expected error: %v, but got none", test.expectedError)
			} else if err != nil && test.expectedError != nil {
				if err.Error() != test.expectedError.Error() {
					t.Errorf("Expected error: %v, got: %v", test.expectedError, err)
				}
			}

			if p.state != test.expectedResult {
				t.Errorf("Expected state: %v, got: %v", test.expectedResult, p.state)
			}
		})
	}
}
