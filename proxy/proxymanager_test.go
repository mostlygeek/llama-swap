package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

		_, exists := proxy.currentProcesses[ProcessKeyName("", modelName)]
		assert.True(t, exists, "expected %s key in currentProcesses", modelName)

	}

	// make sure there's only one loaded model
	assert.Len(t, proxy.currentProcesses, 1)
}

func TestProxyManager_SwapMultiProcess(t *testing.T) {

	model1 := "path1/model1"
	model2 := "path2/model2"

	profileModel1 := ProcessKeyName("test", model1)
	profileModel2 := ProcessKeyName("test", model2)

	config := &Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			model1: getTestSimpleResponderConfig("model1"),
			model2: getTestSimpleResponderConfig("model2"),
		},
		Profiles: map[string][]string{
			"test": {model1, model2},
		},
	}

	proxy := New(config)
	defer proxy.StopProcesses()

	for modelID, requestedModel := range map[string]string{
		"model1": profileModel1,
		"model2": profileModel2,
	} {
		reqBody := fmt.Sprintf(`{"model":"%s"}`, requestedModel)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.HandlerFunc(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), modelID)
	}

	// make sure there's two loaded models
	assert.Len(t, proxy.currentProcesses, 2)
	_, exists := proxy.currentProcesses[profileModel1]
	assert.True(t, exists, "expected "+profileModel1+" key in currentProcesses")

	_, exists = proxy.currentProcesses[profileModel2]
	assert.True(t, exists, "expected "+profileModel2+" key in currentProcesses")
}

// When a request for a different model comes in ProxyManager should wait until
// the first request is complete before swapping. Both requests should complete
func TestProxyManager_SwapMultiProcessParallelRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	config := &Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
			"model3": getTestSimpleResponderConfig("model3"),
		},
	}

	proxy := New(config)
	defer proxy.StopProcesses()

	results := map[string]string{}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for key := range config.Models {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()

			reqBody := fmt.Sprintf(`{"model":"%s"}`, key)
			req := httptest.NewRequest("POST", "/v1/chat/completions?wait=1000ms", bytes.NewBufferString(reqBody))
			w := httptest.NewRecorder()

			proxy.HandlerFunc(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status OK, got %d for key %s", w.Code, key)
			}

			mu.Lock()

			results[key] = w.Body.String()
			mu.Unlock()
		}(key)

		<-time.After(time.Millisecond)
	}

	wg.Wait()
	assert.Len(t, results, len(config.Models))

	for key, result := range results {
		assert.Equal(t, key, result)
	}
}

func TestProxyManager_ListModelsHandler(t *testing.T) {
	config := &Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
			"model3": getTestSimpleResponderConfig("model3"),
		},
	}

	proxy := New(config)

	// Create a test request
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Add("Origin", "i-am-the-origin")
	w := httptest.NewRecorder()

	// Call the listModelsHandler
	proxy.HandlerFunc(w, req)

	// Check the response status code
	assert.Equal(t, http.StatusOK, w.Code)

	// Check for Access-Control-Allow-Origin
	assert.Equal(t, req.Header.Get("Origin"), w.Result().Header.Get("Access-Control-Allow-Origin"))

	// Parse the JSON response
	var response struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	// Check the number of models returned
	assert.Len(t, response.Data, 3)

	// Check the details of each model
	expectedModels := map[string]struct{}{
		"model1": {},
		"model2": {},
		"model3": {},
	}

	for _, model := range response.Data {
		modelID, ok := model["id"].(string)
		assert.True(t, ok, "model ID should be a string")
		_, exists := expectedModels[modelID]
		assert.True(t, exists, "unexpected model ID: %s", modelID)
		delete(expectedModels, modelID)

		object, ok := model["object"].(string)
		assert.True(t, ok, "object should be a string")
		assert.Equal(t, "model", object)

		created, ok := model["created"].(float64)
		assert.True(t, ok, "created should be a number")
		assert.Greater(t, created, float64(0)) // Assuming the timestamp is positive

		ownedBy, ok := model["owned_by"].(string)
		assert.True(t, ok, "owned_by should be a string")
		assert.Equal(t, "llama-swap", ownedBy)
	}

	// Ensure all expected models were returned
	assert.Empty(t, expectedModels, "not all expected models were returned")
}

func TestProxyManager_ProfileNonMember(t *testing.T) {

	model1 := "path1/model1"
	model2 := "path2/model2"

	profileMemberName := ProcessKeyName("test", model1)
	profileNonMemberName := ProcessKeyName("test", model2)

	config := &Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			model1: getTestSimpleResponderConfig("model1"),
			model2: getTestSimpleResponderConfig("model2"),
		},
		Profiles: map[string][]string{
			"test": {model1},
		},
	}

	proxy := New(config)
	defer proxy.StopProcesses()

	// actual member of profile
	{
		reqBody := fmt.Sprintf(`{"model":"%s"}`, profileMemberName)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.HandlerFunc(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "model1")
	}

	// actual model, but non-member will 404
	{
		reqBody := fmt.Sprintf(`{"model":"%s"}`, profileNonMemberName)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.HandlerFunc(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	}
}

func TestProxyManager_Shutdown(t *testing.T) {
	// make broken model configurations
	model1Config := getTestSimpleResponderConfigPort("model1", 9991)
	model1Config.Proxy = "http://localhost:10001/"

	model2Config := getTestSimpleResponderConfigPort("model2", 9992)
	model2Config.Proxy = "http://localhost:10002/"

	model3Config := getTestSimpleResponderConfigPort("model3", 9993)
	model3Config.Proxy = "http://localhost:10003/"

	config := &Config{
		HealthCheckTimeout: 15,
		Profiles: map[string][]string{
			"test": {"model1", "model2", "model3"},
		},
		Models: map[string]ModelConfig{
			"model1": model1Config,
			"model2": model2Config,
			"model3": model3Config,
		},
	}

	proxy := New(config)

	// Start all the processes
	var wg sync.WaitGroup
	for _, modelName := range []string{"test:model1", "test:model2", "test:model3"} {
		wg.Add(1)
		go func(modelName string) {
			defer wg.Done()
			reqBody := fmt.Sprintf(`{"model":"%s"}`, modelName)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
			w := httptest.NewRecorder()

			// send a request to trigger the proxy to load
			proxy.HandlerFunc(w, req)
			assert.Equal(t, http.StatusBadGateway, w.Code)
			assert.Contains(t, w.Body.String(), "health check interrupted due to shutdown")
			//fmt.Println(w.Code, w.Body.String())
		}(modelName)
	}

	go func() {
		<-time.After(time.Second)
		proxy.Shutdown()
	}()
	wg.Wait()
}
