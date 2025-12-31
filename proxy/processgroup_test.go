package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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
