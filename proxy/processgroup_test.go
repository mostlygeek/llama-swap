package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var processGroupTestConfig = config.AddDefaultGroupToConfig(config.Config{
	HealthCheckTimeout: 15,
	Models: map[string]config.ModelConfig{
		"model1": getTestSimpleResponderConfig("model1"),
		"model2": getTestSimpleResponderConfig("model2"),
		"model3": getTestSimpleResponderConfig("model3"),
		"model4": getTestSimpleResponderConfig("model4"),
		"model5": getTestSimpleResponderConfig("model5"),
	},
	Groups: map[string]config.GroupConfig{
		"G1": {
			Swap:      true,
			Exclusive: true,
			Members:   []string{"model1", "model2"},
		},
		"G2": {
			Swap:      false,
			Exclusive: true,
			Members:   []string{"model3", "model4"},
		},
	},
})

func TestProcessGroup_DefaultHasCorrectModel(t *testing.T) {
	pg := NewProcessGroup(config.DEFAULT_GROUP_ID, processGroupTestConfig, testLogger, testLogger)
	assert.True(t, pg.HasMember("model5"))
}

func TestProcessGroup_HasMember(t *testing.T) {
	pg := NewProcessGroup("G1", processGroupTestConfig, testLogger, testLogger)
	assert.True(t, pg.HasMember("model1"))
	assert.True(t, pg.HasMember("model2"))
	assert.False(t, pg.HasMember("model3"))
}

// TestProcessGroup_ProxyRequestSwapIsTrueParallel tests that when swap is true
// and multiple requests are made in parallel, only one process is running at a time.
func TestProcessGroup_ProxyRequestSwapIsTrueParallel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	var processGroupTestConfig = config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			// use the same listening so if a model is already running, it will fail
			// this is a way to test that swap isolation is working
			// properly when there are parallel requests made at the
			// same time.
			"model1": getTestSimpleResponderConfigPort("model1", 9832),
			"model2": getTestSimpleResponderConfigPort("model2", 9832),
			"model3": getTestSimpleResponderConfigPort("model3", 9832),
			"model4": getTestSimpleResponderConfigPort("model4", 9832),
			"model5": getTestSimpleResponderConfigPort("model5", 9832),
		},
		Groups: map[string]config.GroupConfig{
			"G1": {
				Swap:    true,
				Members: []string{"model1", "model2", "model3", "model4", "model5"},
			},
		},
	})

	pg := NewProcessGroup("G1", processGroupTestConfig, testLogger, testLogger)
	defer pg.StopProcesses(StopWaitForInflightRequest)

	tests := []string{"model1", "model2", "model3", "model4", "model5"}

	var wg sync.WaitGroup

	wg.Add(len(tests))
	for _, modelName := range tests {
		go func(modelName string) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			assert.NoError(t, pg.ProxyRequest(modelName, w, req))
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), modelName)
		}(modelName)
	}
	wg.Wait()
}

// TestProcessGroup_ProxyRequestSwapRaceAgainstFastPath verifies that a swap
// request cannot stop the current process while a fast-path request (for the
// already-selected model) is in flight. Without ProcessGroup-level inflight
// tracking, a fast-path request that has released pg.Lock but has not yet
// incremented Process.inFlightRequests races with Stop()'s Wait() and the
// process is killed mid-request.
func TestProcessGroup_ProxyRequestSwapRaceAgainstFastPath(t *testing.T) {
	cfg := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		Groups: map[string]config.GroupConfig{
			"G1": {
				Swap:    true,
				Members: []string{"model1", "model2"},
			},
		},
	})

	pg := NewProcessGroup("G1", cfg, testLogger, testLogger)
	defer pg.StopProcesses(StopImmediately)

	// Bypass real subprocesses so the test is fast and deterministic.
	pg.processes["model1"].testHandler = newTestHandler("model1")
	pg.processes["model2"].testHandler = newTestHandler("model2")

	// Prime: run a request through model1 via the swap path so that
	// lastUsedProcess == "model1" and subsequent model1 requests take the
	// fast path.
	primeReq := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	primeW := httptest.NewRecorder()
	require.NoError(t, pg.ProxyRequest("model1", primeW, primeReq))
	require.Equal(t, http.StatusOK, primeW.Code)
	require.Equal(t, StateReady, pg.processes["model1"].CurrentState())
	require.Equal(t, StateStopped, pg.processes["model2"].CurrentState())

	// Fast-path hook: signal arrival at the race window, then wait for
	// release. This parks R2 deterministically at the point where pg.Lock
	// has been released but Process.inFlightRequests has not yet been
	// incremented — the exact window the race exploits.
	r2Reached := make(chan struct{})
	r2Release := make(chan struct{})
	pg.testDelayFastPath = func() {
		close(r2Reached)
		<-r2Release
	}

	// R2: fast-path request for model1. Will pause at the test hook.
	r2Done := make(chan struct{})
	w2 := httptest.NewRecorder()
	go func() {
		defer close(r2Done)
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		assert.NoError(t, pg.ProxyRequest("model1", w2, req))
	}()

	// Deterministically wait for R2 to reach the race window.
	<-r2Reached

	// R3: swap request for model2. Must wait for R2 to finish before touching
	// model1, otherwise model1 gets killed mid-request.
	r3Done := make(chan struct{})
	w3 := httptest.NewRecorder()
	go func() {
		defer close(r3Done)
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		assert.NoError(t, pg.ProxyRequest("model2", w3, req))
	}()

	// Spin until R3 has acquired pg.Lock and entered the swap critical
	// section. In the fixed code, R3 then blocks on pg.inflight.Wait() while
	// still holding the lock, so TryLock keeps failing.
	for pg.TryLock() {
		pg.Unlock()
		runtime.Gosched()
	}

	// Bounded poll: give R3 a chance to demonstrate the bug by mutating
	// state. In the fixed code, R3 is blocked on pg.inflight.Wait() and
	// nothing changes, so we wait the full window. In the buggy code, R3
	// will Stop() model1 and start serving via model2 within microseconds —
	// we exit early once the mutation is observable.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if pg.processes["model1"].CurrentState() != StateReady ||
			pg.processes["model2"].CurrentState() != StateStopped {
			break
		}
		done := false
		select {
		case <-r3Done:
			done = true
		default:
		}
		if done {
			break
		}
		runtime.Gosched()
	}

	// Invariant: R3 must be blocked while R2 is still in flight.
	select {
	case <-r3Done:
		t.Fatal("swap completed while fast-path request was still in flight — race not prevented")
	default:
	}
	assert.Equal(t, StateReady, pg.processes["model1"].CurrentState(),
		"model1 must stay Ready while a fast-path request is in flight")
	assert.Equal(t, StateStopped, pg.processes["model2"].CurrentState(),
		"model2 must not be started until R2 finishes and model1 is swapped out")

	// Release R2 and let both requests finish.
	close(r2Release)
	<-r2Done
	<-r3Done

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), "model1")
	assert.Equal(t, http.StatusOK, w3.Code)
	assert.Contains(t, w3.Body.String(), "model2")
}

func TestProcessGroup_ProxyRequestSwapIsFalse(t *testing.T) {
	pg := NewProcessGroup("G2", processGroupTestConfig, testLogger, testLogger)
	defer pg.StopProcesses(StopWaitForInflightRequest)

	tests := []string{"model3", "model4"}

	for _, modelName := range tests {
		t.Run(modelName, func(t *testing.T) {
			reqBody := `{"x", "y"}`
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
			w := httptest.NewRecorder()
			assert.NoError(t, pg.ProxyRequest(modelName, w, req))
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), modelName)
		})
	}

	// make sure all the processes are running
	for _, process := range pg.processes {
		assert.Equal(t, StateReady, process.CurrentState())
	}
}
