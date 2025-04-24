package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	debugLogger = NewLogMonitorWriter(os.Stdout)
)

func init() {
	// flip to help with debugging tests
	if false {
		debugLogger.SetLogLevel(LevelDebug)
	} else {
		debugLogger.SetLogLevel(LevelError)
	}
}

func TestProcess_AutomaticallyStartsUpstream(t *testing.T) {

	expectedMessage := "testing91931"
	config := getTestSimpleResponderConfig(expectedMessage)

	// Create a process
	process := NewProcess("test-process", 5, config, debugLogger, debugLogger)
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

// TestProcess_WaitOnMultipleStarts tests that multiple concurrent requests
// are all handled successfully, even though they all may ask for the process to .start()
func TestProcess_WaitOnMultipleStarts(t *testing.T) {

	expectedMessage := "testing91931"
	config := getTestSimpleResponderConfig(expectedMessage)

	process := NewProcess("test-process", 5, config, debugLogger, debugLogger)
	defer process.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(reqID int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()
			process.ProxyRequest(w, req)
			assert.Equal(t, http.StatusOK, w.Code, "Worker %d got wrong HTTP code", reqID)
			assert.Contains(t, w.Body.String(), expectedMessage, "Worker %d got wrong message", reqID)
		}(i)
	}
	wg.Wait()
	assert.Equal(t, StateReady, process.CurrentState())
}

// test that the automatic start returns the expected error type
func TestProcess_BrokenModelConfig(t *testing.T) {
	// Create a process configuration
	config := ModelConfig{
		Cmd:           "nonexistent-command",
		Proxy:         "http://127.0.0.1:9913",
		CheckEndpoint: "/health",
	}

	process := NewProcess("broken", 1, config, debugLogger, debugLogger)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	process.ProxyRequest(w, req)
	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "unable to start process")

	w = httptest.NewRecorder()
	process.ProxyRequest(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "Process can not ProxyRequest, state is failed")
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

	process := NewProcess("ttl_test", 2, config, debugLogger, debugLogger)
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

	process := NewProcess("ttl", 2, config, debugLogger, debugLogger)
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
// This test makes sure using Process.Stop() does not affect pending HTTP
// requests. All HTTP requests in this test should complete successfully.
func TestProcess_HTTPRequestsHaveTimeToFinish(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	expectedMessage := "12345"
	config := getTestSimpleResponderConfig(expectedMessage)
	process := NewProcess("t", 10, config, debugLogger, debugLogger)
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
			// send a request where simple-responder is will wait 300ms before responding
			// this will simulate an in-progress request.
			req := httptest.NewRequest("GET", fmt.Sprintf("/slow-respond?echo=%s&delay=300ms", key), nil)
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

	// Stop the process while requests are still being processed
	go func() {
		<-time.After(150 * time.Millisecond)
		process.Stop()
	}()

	wg.Wait()

	for key, result := range results {
		assert.Equal(t, key, result)
	}
}

func TestProcess_SwapState(t *testing.T) {
	tests := []struct {
		name           string
		currentState   ProcessState
		expectedState  ProcessState
		newState       ProcessState
		expectedError  error
		expectedResult ProcessState
	}{
		{"Stopped to Starting", StateStopped, StateStopped, StateStarting, nil, StateStarting},
		{"Starting to Ready", StateStarting, StateStarting, StateReady, nil, StateReady},
		{"Starting to Failed", StateStarting, StateStarting, StateFailed, nil, StateFailed},
		{"Starting to Stopping", StateStarting, StateStarting, StateStopping, nil, StateStopping},
		{"Ready to Stopping", StateReady, StateReady, StateStopping, nil, StateStopping},
		{"Stopping to Stopped", StateStopping, StateStopping, StateStopped, nil, StateStopped},
		{"Stopping to Shutdown", StateStopping, StateStopping, StateShutdown, nil, StateShutdown},
		{"Stopped to Ready", StateStopped, StateStopped, StateReady, ErrInvalidStateTransition, StateStopped},
		{"Starting to Stopped", StateStarting, StateStarting, StateStopped, ErrInvalidStateTransition, StateStarting},
		{"Ready to Starting", StateReady, StateReady, StateStarting, ErrInvalidStateTransition, StateReady},
		{"Ready to Failed", StateReady, StateReady, StateFailed, ErrInvalidStateTransition, StateReady},
		{"Stopping to Ready", StateStopping, StateStopping, StateReady, ErrInvalidStateTransition, StateStopping},
		{"Failed to Stopped", StateFailed, StateFailed, StateStopped, ErrInvalidStateTransition, StateFailed},
		{"Failed to Starting", StateFailed, StateFailed, StateStarting, ErrInvalidStateTransition, StateFailed},
		{"Shutdown to Stopped", StateShutdown, StateShutdown, StateStopped, ErrInvalidStateTransition, StateShutdown},
		{"Shutdown to Starting", StateShutdown, StateShutdown, StateStarting, ErrInvalidStateTransition, StateShutdown},
		{"Expected state mismatch", StateStopped, StateStarting, StateStarting, ErrExpectedStateMismatch, StateStopped},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := NewProcess("test", 10, getTestSimpleResponderConfig("test"), debugLogger, debugLogger)
			p.state = test.currentState

			resultState, err := p.swapState(test.expectedState, test.newState)
			if err != nil && test.expectedError == nil {
				t.Errorf("Unexpected error: %v", err)
			} else if err == nil && test.expectedError != nil {
				t.Errorf("Expected error: %v, but got none", test.expectedError)
			} else if err != nil && test.expectedError != nil {
				if err.Error() != test.expectedError.Error() {
					t.Errorf("Expected error: %v, got: %v", test.expectedError, err)
				}
			}

			if resultState != test.expectedResult {
				t.Errorf("Expected state: %v, got: %v", test.expectedResult, resultState)
			}
		})
	}
}

func TestProcess_ShutdownInterruptsHealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long shutdown test")
	}

	expectedMessage := "testing91931"

	// make a config where the healthcheck will always fail because port is wrong
	config := getTestSimpleResponderConfigPort(expectedMessage, 9999)
	config.Proxy = "http://localhost:9998/test"

	healthCheckTTLSeconds := 30
	process := NewProcess("test-process", healthCheckTTLSeconds, config, debugLogger, debugLogger)

	// make it a lot faster
	process.healthCheckLoopInterval = time.Second

	// start a goroutine to simulate a shutdown
	var wg sync.WaitGroup
	go func() {
		defer wg.Done()
		<-time.After(time.Millisecond * 500)
		process.Shutdown()
	}()
	wg.Add(1)

	// start the process, this is a blocking call
	err := process.start()

	wg.Wait()
	assert.ErrorContains(t, err, "health check interrupted due to shutdown")
	assert.Equal(t, StateShutdown, process.CurrentState())
}

func TestProcess_ExitInterruptsHealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Exit Interrupts Health Check test")
	}

	// should run and exit but interrupt the long checkHealthTimeout
	checkHealthTimeout := 5
	config := ModelConfig{
		Cmd:           "sleep 1",
		Proxy:         "http://127.0.0.1:9913",
		CheckEndpoint: "/health",
	}

	process := NewProcess("sleepy", checkHealthTimeout, config, debugLogger, debugLogger)
	process.healthCheckLoopInterval = time.Second // make it faster
	err := process.start()
	assert.Equal(t, "upstream command exited prematurely with no error", err.Error())
	assert.Equal(t, process.CurrentState(), StateFailed)
}
