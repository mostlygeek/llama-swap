package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProxyManager_SwapProcessCorrectly(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses()

	for _, modelName := range []string{"model1", "model2"} {
		reqBody := fmt.Sprintf(`{"model":"%s"}`, modelName)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), modelName)
	}
}

func TestProxyManager_SwapMultiProcess(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		LogLevel: "error",
		Groups: map[string]GroupConfig{
			"G1": {
				Swap:      true,
				Exclusive: false,
				Members:   []string{"model1"},
			},
			"G2": {
				Swap:      true,
				Exclusive: false,
				Members:   []string{"model2"},
			},
		},
	})

	proxy := New(config)
	defer proxy.StopProcesses()

	tests := []string{"model1", "model2"}
	for _, requestedModel := range tests {
		t.Run(requestedModel, func(t *testing.T) {
			reqBody := fmt.Sprintf(`{"model":"%s"}`, requestedModel)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
			w := httptest.NewRecorder()

			proxy.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), requestedModel)
		})
	}

	// make sure there's two loaded models
	assert.Equal(t, proxy.findGroupByModelName("model1").processes["model1"].CurrentState(), StateReady)
	assert.Equal(t, proxy.findGroupByModelName("model2").processes["model2"].CurrentState(), StateReady)
}

// Test that a persistent group is not affected by the swapping behaviour of
// other groups.
func TestProxyManager_PersistentGroupsAreNotSwapped(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"), // goes into the default group
			"model2": getTestSimpleResponderConfig("model2"),
		},
		LogLevel: "error",
		Groups: map[string]GroupConfig{
			// the forever group is persistent and should not be affected by model1
			"forever": {
				Swap:       true,
				Exclusive:  false,
				Persistent: true,
				Members:    []string{"model2"},
			},
		},
	})

	proxy := New(config)
	defer proxy.StopProcesses()

	// make requests to load all models, loading model1 should not affect model2
	tests := []string{"model2", "model1"}
	for _, requestedModel := range tests {
		reqBody := fmt.Sprintf(`{"model":"%s"}`, requestedModel)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), requestedModel)
	}

	assert.Equal(t, proxy.findGroupByModelName("model2").processes["model2"].CurrentState(), StateReady)
	assert.Equal(t, proxy.findGroupByModelName("model1").processes["model1"].CurrentState(), StateReady)
}

// When a request for a different model comes in ProxyManager should wait until
// the first request is complete before swapping. Both requests should complete
func TestProxyManager_SwapMultiProcessParallelRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
			"model3": getTestSimpleResponderConfig("model3"),
		},
		LogLevel: "error",
	})

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

			proxy.ServeHTTP(w, req)

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
	config := Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
			"model3": getTestSimpleResponderConfig("model3"),
		},
		LogLevel: "error",
	}

	proxy := New(config)

	// Create a test request
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Add("Origin", "i-am-the-origin")
	w := httptest.NewRecorder()

	// Call the listModelsHandler
	proxy.ServeHTTP(w, req)

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

func TestProxyManager_AudioTranscriptionHandler(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"TheExpectedModel": getTestSimpleResponderConfig("TheExpectedModel"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses()

	// Create a buffer with multipart form data
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	// Add the model field
	fw, err := w.CreateFormField("model")
	assert.NoError(t, err)
	_, err = fw.Write([]byte("TheExpectedModel"))
	assert.NoError(t, err)

	// Add a file field
	fw, err = w.CreateFormFile("file", "test.mp3")
	assert.NoError(t, err)
	// Generate random content length between 10 and 20
	contentLength := rand.Intn(11) + 10 // 10 to 20
	content := make([]byte, contentLength)
	_, err = fw.Write(content)
	assert.NoError(t, err)
	w.Close()

	// Create the request with the multipart form data
	req := httptest.NewRequest("POST", "/v1/audio/transcriptions", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	// Verify the response
	assert.Equal(t, http.StatusOK, rec.Code)
	var response map[string]string
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "TheExpectedModel", response["model"])
	assert.Equal(t, response["text"], fmt.Sprintf("The length of the file is %d bytes", contentLength)) // matches simple-responder
}

func TestProxyManager_Shutdown(t *testing.T) {
	// Test Case 1: Startup failure due to unavailable proxy
	t.Run("startup failure with unavailable proxy", func(t *testing.T) {
		// Create configuration with invalid command that will fail immediately
		modelConfig := ModelConfig{
			Cmd:           "/invalid-command", // Invalid executable path that will fail to start
			Proxy:         "http://localhost:9991",
			CheckEndpoint: "/health",
		}

		config := AddDefaultGroupToConfig(Config{
			HealthCheckTimeout: 15,
			Models: map[string]ModelConfig{
				"model1": modelConfig,
			},
			LogLevel: "error",
		})

		proxy := New(config)
		defer proxy.Shutdown()

		// Try to start the model
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadGateway, w.Code)
		assert.Contains(t, w.Body.String(), "unable to start process: start() failed: fork/exec /invalid-command: no such file or directory")

		// Verify process is tracked but in failed state
		processGroup := proxy.findGroupByModelName("model1")
		assert.NotNil(t, processGroup)
		process := processGroup.processes["model1"]
		assert.Equal(t, StateFailed, process.CurrentState())
	})
}

func TestProxyManager_Unload(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	reqBody := fmt.Sprintf(`{"model":"%s"}`, "model1")
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, proxy.processGroups[DEFAULT_GROUP_ID].processes["model1"].CurrentState(), StateReady)
	req = httptest.NewRequest("GET", "/unload", nil)
	w = httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, w.Body.String(), "OK")

	// give it a bit of time to stop
	<-time.After(time.Millisecond * 250)
	assert.Equal(t, proxy.processGroups[DEFAULT_GROUP_ID].processes["model1"].CurrentState(), StateStopped)
}

// Test issue #61 `Listing the current list of models and the loaded model.`
func TestProxyManager_RunningEndpoint(t *testing.T) {
	// Shared configuration
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		LogLevel: "debug",
	})

	// Define a helper struct to parse the JSON response.
	type RunningResponse struct {
		Running []struct {
			Model string `json:"model"`
			State string `json:"state"`
		} `json:"running"`
	}

	// Create proxy once for all tests
	proxy := New(config)
	defer proxy.StopProcesses()

	t.Run("no models loaded", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/running", nil)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response RunningResponse

		// Check if this is a valid JSON object.
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

		// We should have an empty running array here.
		assert.Empty(t, response.Running, "expected no running models")
	})

	t.Run("single model loaded", func(t *testing.T) {
		// Load just a model.
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		// Simulate browser call for the `/running` endpoint.
		req = httptest.NewRequest("GET", "/running", nil)
		w = httptest.NewRecorder()
		proxy.ServeHTTP(w, req)

		var response RunningResponse
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

		// Check if we have a single array element.
		assert.Len(t, response.Running, 1)

		// Is this the right model?
		assert.Equal(t, "model1", response.Running[0].Model)

		// Is the model loaded?
		assert.Equal(t, "ready", response.Running[0].State)
	})
}

func TestProxyManager_CORSOptionsHandler(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	tests := []struct {
		name            string
		method          string
		requestHeaders  map[string]string
		expectedStatus  int
		expectedHeaders map[string]string
	}{
		{
			name:           "OPTIONS with no headers",
			method:         "OPTIONS",
			expectedStatus: http.StatusNoContent,
			expectedHeaders: map[string]string{
				"Access-Control-Allow-Origin":  "*",
				"Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
				"Access-Control-Allow-Headers": "Content-Type, Authorization, Accept, X-Requested-With",
			},
		},
		{
			name:   "OPTIONS with specific headers",
			method: "OPTIONS",
			requestHeaders: map[string]string{
				"Access-Control-Request-Headers": "X-Custom-Header, Some-Other-Header",
			},
			expectedStatus: http.StatusNoContent,
			expectedHeaders: map[string]string{
				"Access-Control-Allow-Origin":  "*",
				"Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
				"Access-Control-Allow-Headers": "X-Custom-Header, Some-Other-Header",
			},
		},
		{
			name:           "Non-OPTIONS request",
			method:         "GET",
			expectedStatus: http.StatusNotFound, // Since we don't have a GET route defined
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := New(config)
			defer proxy.StopProcesses()

			req := httptest.NewRequest(tt.method, "/v1/chat/completions", nil)
			for k, v := range tt.requestHeaders {
				req.Header.Set(k, v)
			}

			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			for header, expectedValue := range tt.expectedHeaders {
				assert.Equal(t, expectedValue, w.Header().Get(header))
			}
		})
	}
}

// Test useModelName in configuration sends overrides what is sent to upstream
func TestProxyManager_UseModelName(t *testing.T) {
	upstreamModelName := "upstreamModel"

	modelConfig := getTestSimpleResponderConfig(upstreamModelName)
	modelConfig.UseModelName = upstreamModelName

	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": modelConfig,
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses()

	requestedModel := "model1"

	t.Run("useModelName over rides requested model: /v1/chat/completions", func(t *testing.T) {
		reqBody := fmt.Sprintf(`{"model":"%s"}`, requestedModel)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), upstreamModelName)
	})

	t.Run("useModelName over rides requested model: /v1/audio/transcriptions", func(t *testing.T) {
		// Create a buffer with multipart form data
		var b bytes.Buffer
		w := multipart.NewWriter(&b)

		// Add the model field
		fw, err := w.CreateFormField("model")
		assert.NoError(t, err)
		_, err = fw.Write([]byte(requestedModel))
		assert.NoError(t, err)

		// Add a file field
		fw, err = w.CreateFormFile("file", "test.mp3")
		assert.NoError(t, err)
		_, err = fw.Write([]byte("test"))
		assert.NoError(t, err)
		w.Close()

		// Create the request with the multipart form data
		req := httptest.NewRequest("POST", "/v1/audio/transcriptions", &b)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		// Verify the response
		assert.Equal(t, http.StatusOK, rec.Code)
		var response map[string]string
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Equal(t, upstreamModelName, response["model"])
	})
}

func TestProxyManager_Upstream(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses()
	req := httptest.NewRequest("GET", "/upstream/model1/test", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "model1", rec.Body.String())
}
