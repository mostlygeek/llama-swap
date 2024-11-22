package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProxyManager_SwapProcessCorrectly(t *testing.T) {
	config := &Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
	}

	proxy := New(config)
	defer proxy.StopProcesses()

	for _, modelName := range []string{"model1", "model2"} {
		reqBody := fmt.Sprintf(`{"model":"%s"}`, modelName)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.HandlerFunc(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), modelName)

		_, exists := proxy.currentProcesses["/"+modelName]
		assert.True(t, exists, "expected %s key in currentProcesses", modelName)

	}

	// make sure there's only one loaded model
	assert.Len(t, proxy.currentProcesses, 1)
}

func TestProxyManager_SwapMultiProcess(t *testing.T) {
	config := &Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		Groups: map[string][]string{
			"test": {"model1", "model2"},
		},
	}

	proxy := New(config)
	defer proxy.StopProcesses()

	for modelID, requestedModel := range map[string]string{"model1": "test/model1", "model2": "test/model2"} {
		reqBody := fmt.Sprintf(`{"model":"%s"}`, requestedModel)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.HandlerFunc(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), modelID)
	}

	// make sure there's two loaded models
	assert.Len(t, proxy.currentProcesses, 2)
	_, exists := proxy.currentProcesses["test/model1"]
	assert.True(t, exists, "expected test/model1 key in currentProcesses")

	_, exists = proxy.currentProcesses["test/model2"]
	assert.True(t, exists, "expected test/model2 key in currentProcesses")

}
