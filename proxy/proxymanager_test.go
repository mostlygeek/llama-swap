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
	config := &Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		LogLevel: "error",
	}

	proxy := New(config)
	defer proxy.StopProcesses()

	for _, modelName := range []string{"model1", "model2"} {
		reqBody := fmt.Sprintf(`{"model":"%s"}`, modelName)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.ServeHTTP(w, req)
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
		LogLevel: "error",
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

		proxy.ServeHTTP(w, req)
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
		LogLevel: "error",
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
	config := &Config{
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
		LogLevel: "error",
	}

	proxy := New(config)
	defer proxy.StopProcesses()

	// actual member of profile
	{
		reqBody := fmt.Sprintf(`{"model":"%s"}`, profileMemberName)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "model1")
	}

	// actual model, but non-member will 404
	{
		reqBody := fmt.Sprintf(`{"model":"%s"}`, profileNonMemberName)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	}
}

func TestProxyManager_Shutdown(t *testing.T) {
	// Test Case 1: Startup failure due to unavailable proxy
	t.Run("startup failure with unavailable proxy", func(t *testing.T) {
		// Create configuration with invalid proxy URL
		modelConfig := getTestSimpleResponderConfigPort("model1", 9991)
		modelConfig.Proxy = "http://localhost:10001/" // Invalid proxy URL

		config := &Config{
			HealthCheckTimeout: 15,
			Models: map[string]ModelConfig{
				"model1": modelConfig,
			},
			LogLevel: "error",
		}

		proxy := New(config)
		defer proxy.Shutdown()

		// Try to start the model
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, req)

		// Verify response
		assert.Equal(t, http.StatusBadGateway, w.Code)
		assert.Contains(t, w.Body.String(), "unable to start process: upstream command exited unexpectedly: exit status 1")

		// Verify process is tracked but in failed state
		assert.Len(t, proxy.currentProcesses, 1, "failed process should be tracked")
		process := proxy.currentProcesses[ProcessKeyName("", "model1")]
		assert.Equal(t, StateFailed, process.CurrentState(), "process should be in failed state")
	})

	// Test Case 2: Clean shutdown waits for in-flight requests
	t.Run("clean shutdown waits for requests", func(t *testing.T) {
		// Create configuration with valid model
		config := &Config{
			HealthCheckTimeout: 15,
			Models: map[string]ModelConfig{
				"model1": getTestSimpleResponderConfig("model1"),
			},
			LogLevel: "error",
		}

		proxy := New(config)

		// Start a model and keep track of request completion
		requestDone := make(chan bool)
		requestStarted := make(chan bool)

		// Start long-running request in goroutine
		go func() {
			reqBody := fmt.Sprintf(`{"model":"model1"}`)
			req := httptest.NewRequest("POST", "/v1/chat/completions?wait=3000ms", bytes.NewBufferString(reqBody))
			w := httptest.NewRecorder()

			// Signal that request is about to start
			requestStarted <- true

			proxy.ServeHTTP(w, req)

			// Verify request completed successfully
			assert.Equal(t, http.StatusOK, w.Code)
			requestDone <- true
		}()

		// Wait for request to start
		<-requestStarted

		// Start shutdown in goroutine
		shutdownComplete := make(chan bool)
		go func() {
			proxy.StopProcesses()
			shutdownComplete <- true
		}()

		// Verify shutdown waits for request
		select {
		case <-shutdownComplete:
			t.Error("Shutdown completed before request finished")
		case <-time.After(1 * time.Second):
			// Expected: shutdown is still waiting after 1 second
		}

		// Wait for request to complete
		<-requestDone

		// Now shutdown should complete quickly
		select {
		case <-shutdownComplete:
			// Expected: shutdown completes after request is done
		case <-time.After(1 * time.Second):
			t.Error("Shutdown did not complete after request finished")
		}

		// Verify cleanup
		assert.Len(t, proxy.currentProcesses, 0, "no processes should remain after shutdown")
	})
}

func TestProxyManager_Unload(t *testing.T) {
	config := &Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	}

	proxy := New(config)
	proc, err := proxy.swapModel("model1")
	assert.NoError(t, err)
	assert.NotNil(t, proc)

	assert.Len(t, proxy.currentProcesses, 1)
	req := httptest.NewRequest("GET", "/unload", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, w.Body.String(), "OK")
	assert.Len(t, proxy.currentProcesses, 0)
}

// issue 62, strip profile slug from model name
func TestProxyManager_StripProfileSlug(t *testing.T) {
	config := &Config{
		HealthCheckTimeout: 15,
		Profiles: map[string][]string{
			"test": {"TheExpectedModel"}, // TheExpectedModel is default in simple-responder.go
		},
		Models: map[string]ModelConfig{
			"TheExpectedModel": getTestSimpleResponderConfig("TheExpectedModel"),
		},
		LogLevel: "error",
	}

	proxy := New(config)
	defer proxy.StopProcesses()

	reqBody := fmt.Sprintf(`{"model":"%s"}`, "test:TheExpectedModel")
	req := httptest.NewRequest("POST", "/v1/audio/speech", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "ok")
}

// Test issue #61 `Listing the current list of models and the loaded model.`
func TestProxyManager_RunningEndpoint(t *testing.T) {

	// Shared configuration
	config := &Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		Profiles: map[string][]string{
			"test": {"model1", "model2"},
		},
		LogLevel: "error",
	}

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

	t.Run("multiple models via profile", func(t *testing.T) {
		// Load more than one model.
		for _, model := range []string{"model1", "model2"} {
			profileModel := ProcessKeyName("test", model)
			reqBody := fmt.Sprintf(`{"model":"%s"}`, profileModel)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		}

		// Simulate the browser call.
		req := httptest.NewRequest("GET", "/running", nil)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, req)

		var response RunningResponse

		// The JSON response must be valid.
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

		// The response should contain 2 models.
		assert.Len(t, response.Running, 2)

		expectedModels := map[string]struct{}{
			"model1": {},
			"model2": {},
		}

		// Iterate through the models and check their states as well.
		for _, entry := range response.Running {
			_, exists := expectedModels[entry.Model]
			assert.True(t, exists, "unexpected model %s", entry.Model)
			assert.Equal(t, "ready", entry.State)
			delete(expectedModels, entry.Model)
		}

		// Since we deleted each model while testing for its validity we should have no more models in the response.
		assert.Empty(t, expectedModels, "unexpected additional models in response")
	})
}

func TestProxyManager_AudioTranscriptionHandler(t *testing.T) {
	config := &Config{
		HealthCheckTimeout: 15,
		Profiles: map[string][]string{
			"test": {"TheExpectedModel"},
		},
		Models: map[string]ModelConfig{
			"TheExpectedModel": getTestSimpleResponderConfig("TheExpectedModel"),
		},
		LogLevel: "error",
	}

	proxy := New(config)
	defer proxy.StopProcesses()

	testCases := []struct {
		name        string
		modelInput  string
		expectModel string
	}{
		{
			name:        "With Profile Prefix",
			modelInput:  "test:TheExpectedModel",
			expectModel: "TheExpectedModel", // Profile prefix should be stripped
		},
		{
			name:        "Without Profile Prefix",
			modelInput:  "TheExpectedModel",
			expectModel: "TheExpectedModel", // Should remain the same
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a buffer with multipart form data
			var b bytes.Buffer
			w := multipart.NewWriter(&b)

			// Add the model field
			fw, err := w.CreateFormField("model")
			assert.NoError(t, err)
			_, err = fw.Write([]byte(tc.modelInput))
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
			assert.Equal(t, tc.expectModel, response["model"])
			assert.Equal(t, response["text"], fmt.Sprintf("The length of the file is %d bytes", contentLength)) // matches simple-responder
		})
	}
}

func TestProxyManager_SplitRequestedModel(t *testing.T) {

	tests := []struct {
		name            string
		requestedModel  string
		expectedProfile string
		expectedModel   string
	}{
		{"no profile", "gpt-4", "", "gpt-4"},
		{"with profile", "profile1:gpt-4", "profile1", "gpt-4"},
		{"only profile", "profile1:", "profile1", ""},
		{"empty model", ":gpt-4", "", "gpt-4"},
		{"empty profile", ":", "", ""},
		{"no split char", "gpt-4", "", "gpt-4"},
		{"profile and model with delimiter", "profile1:delimiter:gpt-4", "profile1", "delimiter:gpt-4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profileName, modelName := splitRequestedModel(tt.requestedModel)
			if profileName != tt.expectedProfile {
				t.Errorf("splitRequestedModel(%q) = %q, %q; want %q, %q", tt.requestedModel, profileName, modelName, tt.expectedProfile, tt.expectedModel)
			}
			if modelName != tt.expectedModel {
				t.Errorf("splitRequestedModel(%q) = %q, %q; want %q, %q", tt.requestedModel, profileName, modelName, tt.expectedProfile, tt.expectedModel)
			}
		})
	}
}

// Test useModelName in configuration sends overrides what is sent to upstream
func TestProxyManager_UseModelName(t *testing.T) {

	upstreamModelName := "upstreamModel"

	modelConfig := getTestSimpleResponderConfig(upstreamModelName)
	modelConfig.UseModelName = upstreamModelName

	config := &Config{
		HealthCheckTimeout: 15,
		Profiles: map[string][]string{
			"test": {"model1"},
		},

		Models: map[string]ModelConfig{
			"model1": modelConfig,
		},

		LogLevel: "error",
	}

	proxy := New(config)
	defer proxy.StopProcesses()

	tests := []struct {
		description    string
		requestedModel string
	}{
		{"useModelName over rides requested model", "model1"},
		{"useModelName over rides requested profile:model", "test:model1"},
	}

	for _, tt := range tests {
		t.Run(tt.description+": /v1/chat/completions", func(t *testing.T) {
			reqBody := fmt.Sprintf(`{"model":"%s"}`, tt.requestedModel)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
			w := httptest.NewRecorder()

			proxy.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), upstreamModelName)

		})
	}

	for _, tt := range tests {
		t.Run(tt.description+": /v1/audio/transcriptions", func(t *testing.T) {
			// Create a buffer with multipart form data
			var b bytes.Buffer
			w := multipart.NewWriter(&b)

			// Add the model field
			fw, err := w.CreateFormField("model")
			assert.NoError(t, err)
			_, err = fw.Write([]byte(tt.requestedModel))
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
}

func TestProxyManager_CORSOptionsHandler(t *testing.T) {
	config := &Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	}

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
