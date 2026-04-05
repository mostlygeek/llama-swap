package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
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

// TestProcessGroup_isHighestPriorityLocked verifies the priority comparison
// logic without starting any actual processes.
func TestProcessGroup_isHighestPriorityLocked(t *testing.T) {
	lowCfg := getTestSimpleResponderConfig("low")
	lowCfg.Priority = 0
	highCfg := getTestSimpleResponderConfig("high")
	highCfg.Priority = 5

	cfg := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"low":  lowCfg,
			"high": highCfg,
		},
		Groups: map[string]config.GroupConfig{
			"G": {Swap: true, Members: []string{"low", "high"}},
		},
	})
	pg := NewProcessGroup("G", cfg, testLogger, testLogger)

	// No pending requests: both are considered highest priority.
	assert.True(t, pg.isHighestPriorityLocked("low"))
	assert.True(t, pg.isHighestPriorityLocked("high"))

	// Only high pending: low is no longer highest priority.
	pg.pendingCount["high"] = 1
	assert.False(t, pg.isHighestPriorityLocked("low"))
	assert.True(t, pg.isHighestPriorityLocked("high"))

	// Both pending: high wins, low loses.
	pg.pendingCount["low"] = 1
	assert.False(t, pg.isHighestPriorityLocked("low"))
	assert.True(t, pg.isHighestPriorityLocked("high"))

	// Only low pending: low is highest priority again.
	pg.pendingCount["high"] = 0
	assert.True(t, pg.isHighestPriorityLocked("low"))
}

// TestProcessGroup_PriorityPreemptsAfterCurrentRequest verifies that:
//   - A higher-priority model does NOT abort a currently in-flight request of
//     a lower-priority model.
//   - After the lower-priority model's request completes, the higher-priority
//     model runs before any additional lower-priority requests.
func TestProcessGroup_PriorityPreemptsAfterCurrentRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	lowCfg := getTestSimpleResponderConfig("low")
	lowCfg.Priority = 0
	highCfg := getTestSimpleResponderConfig("high")
	highCfg.Priority = 5

	cfg := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"low":  lowCfg,
			"high": highCfg,
		},
		Groups: map[string]config.GroupConfig{
			"swap-group": {Swap: true, Members: []string{"low", "high"}},
		},
	})

	pg := NewProcessGroup("swap-group", cfg, testLogger, testLogger)
	defer pg.StopProcesses(StopWaitForInflightRequest)

	var order []string
	var orderMu sync.Mutex
	appendOrder := func(name string) {
		orderMu.Lock()
		order = append(order, name)
		orderMu.Unlock()
	}

	var wg sync.WaitGroup

	// 1. Start a slow "low" request (300 ms).
	lowStarted := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/v1/chat/completions?wait=300ms", nil)
		w := httptest.NewRecorder()
		close(lowStarted)
		pg.ProxyRequest("low", w, req)
		appendOrder("low1")
	}()

	// Wait until the slow "low" request is in-flight before queueing the rest.
	<-lowStarted
	time.Sleep(50 * time.Millisecond)

	// 2. Queue a "high" request while "low" is still processing.
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		w := httptest.NewRecorder()
		pg.ProxyRequest("high", w, req)
		appendOrder("high1")
	}()

	// Give "high" time to register in pendingCount before "low2" arrives.
	time.Sleep(20 * time.Millisecond)

	// 3. Queue a second "low" request; it should run after "high" due to priority.
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		w := httptest.NewRecorder()
		pg.ProxyRequest("low", w, req)
		appendOrder("low2")
	}()

	wg.Wait()

	// "low1" runs to completion (not aborted), then "high1" (higher priority),
	// then "low2" (no more high-priority competition).
	assert.Equal(t, []string{"low1", "high1", "low2"}, order)
}

// TestProcessGroup_UnloadDelay_HoldsLowerPriority verifies that a model with
// unloadDelay keeps its slot long enough that a lower-priority model must wait
// for the delay to expire before swapping in.
func TestProcessGroup_UnloadDelay_HoldsLowerPriority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	const delaySeconds = 1

	highCfg := getTestSimpleResponderConfig("high")
	highCfg.Priority = 5
	highCfg.UnloadDelay = delaySeconds
	lowCfg := getTestSimpleResponderConfig("low")
	lowCfg.Priority = 0

	cfg := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"high": highCfg,
			"low":  lowCfg,
		},
		Groups: map[string]config.GroupConfig{
			"G": {Swap: true, Members: []string{"high", "low"}},
		},
	})

	pg := NewProcessGroup("G", cfg, testLogger, testLogger)
	defer pg.StopProcesses(StopWaitForInflightRequest)

	// Serve one fast request to "high" so its unload delay starts.
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	assert.NoError(t, pg.ProxyRequest("high", w, req))

	// Now send a request to "low". It must wait for high's delay to expire.
	start := time.Now()
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w2 := httptest.NewRecorder()
	assert.NoError(t, pg.ProxyRequest("low", w2, req2))
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, time.Duration(delaySeconds)*time.Second-100*time.Millisecond,
		"low-priority model should have waited for the unload delay")
}

// TestProcessGroup_UnloadDelay_HigherPriorityBypasses verifies that a model
// with strictly higher priority does not wait for a lower-priority model's
// unload delay.
func TestProcessGroup_UnloadDelay_HigherPriorityBypasses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	const delaySeconds = 10 // intentionally long; high must not wait this long

	lowCfg := getTestSimpleResponderConfig("low")
	lowCfg.Priority = 0
	lowCfg.UnloadDelay = delaySeconds
	highCfg := getTestSimpleResponderConfig("high")
	highCfg.Priority = 5

	cfg := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"low":  lowCfg,
			"high": highCfg,
		},
		Groups: map[string]config.GroupConfig{
			"G": {Swap: true, Members: []string{"low", "high"}},
		},
	})

	pg := NewProcessGroup("G", cfg, testLogger, testLogger)
	defer pg.StopProcesses(StopWaitForInflightRequest)

	// Serve one fast request to "low" so its unload delay starts.
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	assert.NoError(t, pg.ProxyRequest("low", w, req))

	// "high" arrives while "low" is in its delay. It should bypass immediately.
	start := time.Now()
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w2 := httptest.NewRecorder()
	assert.NoError(t, pg.ProxyRequest("high", w2, req2))
	elapsed := time.Since(start)

	assert.Less(t, elapsed, time.Duration(delaySeconds)*time.Second/2,
		"high-priority model should have bypassed the unload delay")
}

// TestProcessGroup_UnloadDelay_TimerReset verifies that a new request arriving
// for the active model while its unload delay is running cancels the current
// timer and starts a fresh one when that request finishes.  A lower-priority
// model must therefore wait for the full delay period from the end of the
// second request, not from the end of the first.
func TestProcessGroup_UnloadDelay_TimerReset(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	const delaySeconds = 1

	highCfg := getTestSimpleResponderConfig("high")
	highCfg.Priority = 5
	highCfg.UnloadDelay = delaySeconds
	lowCfg := getTestSimpleResponderConfig("low")
	lowCfg.Priority = 0

	cfg := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"high": highCfg,
			"low":  lowCfg,
		},
		Groups: map[string]config.GroupConfig{
			"G": {Swap: true, Members: []string{"high", "low"}},
		},
	})

	pg := NewProcessGroup("G", cfg, testLogger, testLogger)
	defer pg.StopProcesses(StopWaitForInflightRequest)

	// First "high" request — after it finishes the delay timer starts.
	assert.NoError(t, pg.ProxyRequest("high",
		httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/chat/completions", nil)))

	// Wait 200ms into the delay, then send a second "high" request.  This
	// cancels the running timer and starts a fresh one when the request ends.
	time.Sleep(200 * time.Millisecond)
	assert.NoError(t, pg.ProxyRequest("high",
		httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/chat/completions", nil)))

	// "low" arrives right after the second "high" finishes.  It must wait for
	// the full reset delay (1 s), not just the remaining ~800 ms of the first.
	start := time.Now()
	assert.NoError(t, pg.ProxyRequest("low",
		httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/chat/completions", nil)))

	assert.GreaterOrEqual(t, time.Since(start),
		time.Duration(delaySeconds)*time.Second-100*time.Millisecond,
		"low should wait for the full delay after the timer was reset by a second high request")
}

// TestProcessGroup_UnloadDelay_EqualPriorityWaits verifies that a model with
// the same priority as the active model does NOT bypass the unload delay.
// Only a strictly higher priority bypasses it.
func TestProcessGroup_UnloadDelay_EqualPriorityWaits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	const delaySeconds = 1

	aCfg := getTestSimpleResponderConfig("a")
	aCfg.Priority = 5
	aCfg.UnloadDelay = delaySeconds
	bCfg := getTestSimpleResponderConfig("b")
	bCfg.Priority = 5 // same priority as "a"

	cfg := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"a": aCfg,
			"b": bCfg,
		},
		Groups: map[string]config.GroupConfig{
			"G": {Swap: true, Members: []string{"a", "b"}},
		},
	})

	pg := NewProcessGroup("G", cfg, testLogger, testLogger)
	defer pg.StopProcesses(StopWaitForInflightRequest)

	assert.NoError(t, pg.ProxyRequest("a",
		httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/chat/completions", nil)))

	// "b" has the same priority as "a"; it must still wait for the delay.
	start := time.Now()
	assert.NoError(t, pg.ProxyRequest("b",
		httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/chat/completions", nil)))

	assert.GreaterOrEqual(t, time.Since(start),
		time.Duration(delaySeconds)*time.Second-100*time.Millisecond,
		"equal-priority model should wait for the unload delay, not bypass it")
}

// TestProcessGroup_UnloadDelay_ZeroMeansNoDelay verifies that unloadDelay: 0
// (the default) produces no artificial hold — a waiting model swaps in as
// soon as the active model's request finishes.
func TestProcessGroup_UnloadDelay_ZeroMeansNoDelay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	aCfg := getTestSimpleResponderConfig("a")
	aCfg.Priority = 5
	aCfg.UnloadDelay = 0 // explicitly disabled
	bCfg := getTestSimpleResponderConfig("b")
	bCfg.Priority = 0

	cfg := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"a": aCfg,
			"b": bCfg,
		},
		Groups: map[string]config.GroupConfig{
			"G": {Swap: true, Members: []string{"a", "b"}},
		},
	})

	pg := NewProcessGroup("G", cfg, testLogger, testLogger)
	defer pg.StopProcesses(StopWaitForInflightRequest)

	assert.NoError(t, pg.ProxyRequest("a",
		httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/chat/completions", nil)))

	// With unloadDelay=0, "b" should swap in without any added wait.
	// The only time spent is process startup; we allow generous headroom.
	start := time.Now()
	assert.NoError(t, pg.ProxyRequest("b",
		httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/chat/completions", nil)))

	assert.Less(t, time.Since(start), 3*time.Second,
		"with unloadDelay=0 there should be no artificial hold before swapping")
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
