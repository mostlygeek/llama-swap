package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

var processGroupTestConfig = AddDefaultGroupToConfig(Config{
	HealthCheckTimeout: 15,
	Models: map[string]ModelConfig{
		"model1": getTestSimpleResponderConfig("model1"),
		"model2": getTestSimpleResponderConfig("model2"),
		"model3": getTestSimpleResponderConfig("model3"),
		"model4": getTestSimpleResponderConfig("model4"),
		"model5": getTestSimpleResponderConfig("model5"),
	},
	Groups: map[string]GroupConfig{
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
	pg := NewProcessGroup(DEFAULT_GROUP_ID, processGroupTestConfig, testLogger, testLogger, nil)
	assert.True(t, pg.HasMember("model5"))
}

func TestProcessGroup_HasMember(t *testing.T) {
	pg := NewProcessGroup("G1", processGroupTestConfig, testLogger, testLogger, nil)
	assert.True(t, pg.HasMember("model1"))
	assert.True(t, pg.HasMember("model2"))
	assert.False(t, pg.HasMember("model3"))
}

func TestProcessGroup_ProxyRequestSwapIsTrue(t *testing.T) {
	pg := NewProcessGroup("G1", processGroupTestConfig, testLogger, testLogger, nil)
	defer pg.StopProcesses(StopWaitForInflightRequest)

	tests := []string{"model1", "model2"}

	for _, modelName := range tests {
		t.Run(modelName, func(t *testing.T) {
			reqBody := `{"x", "y"}`
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
			w := httptest.NewRecorder()

			assert.NoError(t, pg.ProxyRequest(modelName, w, req))
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), modelName)

			// make sure only one process is in the running state
			count := 0
			for _, process := range pg.processes {
				if process.CurrentState() == StateReady {
					count++
				}
			}
			assert.Equal(t, 1, count)
		})
	}
}

func TestProcessGroup_ProxyRequestSwapIsFalse(t *testing.T) {
	pg := NewProcessGroup("G2", processGroupTestConfig, testLogger, testLogger, nil)
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
