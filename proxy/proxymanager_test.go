package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/event"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
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
	defer proxy.StopProcesses(StopWaitForInflightRequest)

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
	defer proxy.StopProcesses(StopWaitForInflightRequest)

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
	defer proxy.StopProcesses(StopWaitForInflightRequest)

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
	defer proxy.StopProcesses(StopWaitForInflightRequest)

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
			var response map[string]interface{}
			assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			result, ok := response["responseMessage"].(string)
			assert.Equal(t, ok, true)
			results[key] = result
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

	model1Config := getTestSimpleResponderConfig("model1")
	model1Config.Name = "Model 1"
	model1Config.Description = "Model 1 description is used for testing"

	model2Config := getTestSimpleResponderConfig("model2")
	model2Config.Name = "     " // empty whitespace only strings will get ignored
	model2Config.Description = "  "

	config := Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": model1Config,
			"model2": model2Config,
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

	// make all models
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

		// check for optional name and description
		if modelID == "model1" {
			name, ok := model["name"].(string)
			assert.True(t, ok, "name should be a string")
			assert.Equal(t, "Model 1", name)
			description, ok := model["description"].(string)
			assert.True(t, ok, "description should be a string")
			assert.Equal(t, "Model 1 description is used for testing", description)
		} else {
			_, exists := model["name"]
			assert.False(t, exists, "unexpected name field for model: %s", modelID)
			_, exists = model["description"]
			assert.False(t, exists, "unexpected description field for model: %s", modelID)
		}
	}

	// Ensure all expected models were returned
	assert.Empty(t, expectedModels, "not all expected models were returned")
}

func TestProxyManager_ListModelsHandler_SortedByID(t *testing.T) {
	// Intentionally add models in non-sorted order and with an unlisted model
	config := Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"zeta":  getTestSimpleResponderConfig("zeta"),
			"alpha": getTestSimpleResponderConfig("alpha"),
			"beta":  getTestSimpleResponderConfig("beta"),
			"hidden": func() ModelConfig {
				mc := getTestSimpleResponderConfig("hidden")
				mc.Unlisted = true
				return mc
			}(),
		},
		LogLevel: "error",
	}

	proxy := New(config)

	// Request models list
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	// We expect only the listed models in sorted order by id
	expectedOrder := []string{"alpha", "beta", "zeta"}
	if assert.Len(t, response.Data, len(expectedOrder), "unexpected number of listed models") {
		got := make([]string, 0, len(response.Data))
		for _, m := range response.Data {
			id, _ := m["id"].(string)
			got = append(got, id)
		}
		assert.Equal(t, expectedOrder, got, "models should be sorted by id ascending")
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

	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": model1Config,
			"model2": model2Config,
			"model3": model3Config,
		},
		LogLevel: "error",
		Groups: map[string]GroupConfig{
			"test": {
				Swap:    false,
				Members: []string{"model1", "model2", "model3"},
			},
		},
	})

	proxy := New(config)

	// Start all the processes
	var wg sync.WaitGroup
	for _, modelName := range []string{"model1", "model2", "model3"} {
		wg.Add(1)
		go func(modelName string) {
			defer wg.Done()
			reqBody := fmt.Sprintf(`{"model":"%s"}`, modelName)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
			w := httptest.NewRecorder()

			// send a request to trigger the proxy to load ... this should hang waiting for start up
			proxy.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadGateway, w.Code)
			assert.Contains(t, w.Body.String(), "health check interrupted due to shutdown")
		}(modelName)
	}

	go func() {
		<-time.After(time.Second)
		proxy.Shutdown()
	}()
	wg.Wait()
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
		LogLevel: "warn",
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
	defer proxy.StopProcesses(StopWaitForInflightRequest)

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

func TestProxyManager_AudioTranscriptionHandler(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"TheExpectedModel": getTestSimpleResponderConfig("TheExpectedModel"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

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
	assert.Equal(t, strconv.Itoa(370+contentLength), response["h_content_length"])
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
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	requestedModel := "model1"

	t.Run("useModelName over rides requested model: /v1/chat/completions", func(t *testing.T) {
		reqBody := fmt.Sprintf(`{"model":"%s"}`, requestedModel)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := httptest.NewRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), upstreamModelName)

		// make sure the content length was set correctly
		// simple-responder will return the content length it got in the response
		body := w.Body.Bytes()
		contentLength := int(gjson.GetBytes(body, "h_content_length").Int())
		assert.Equal(t, len(fmt.Sprintf(`{"model":"%s"}`, upstreamModelName)), contentLength)
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
			defer proxy.StopProcesses(StopWaitForInflightRequest)

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

func TestProxyManager_Upstream(t *testing.T) {
	configStr := fmt.Sprintf(`
logLevel: error
models:
  model1:
    cmd: %s -port ${PORT} -silent -respond model1
    aliases: [model-alias]
`, getSimpleResponderPath())

	config, err := LoadConfigFromReader(strings.NewReader(configStr))
	assert.NoError(t, err)

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)
	t.Run("main model name", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/upstream/model1/test", nil)
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "model1", rec.Body.String())
	})

	t.Run("model alias", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/upstream/model-alias/test", nil)
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "model1", rec.Body.String())
	})
}

func TestProxyManager_ChatContentLength(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	reqBody := fmt.Sprintf(`{"model":"%s", "x": "this is just some content to push the length out a bit"}`, "model1")
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "81", response["h_content_length"])
	assert.Equal(t, "model1", response["responseMessage"])
}

func TestProxyManager_FiltersStripParams(t *testing.T) {
	modelConfig := getTestSimpleResponderConfig("model1")
	modelConfig.Filters = ModelFilters{
		StripParams: "temperature, model, stream",
	}

	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		LogLevel:           "error",
		Models: map[string]ModelConfig{
			"model1": modelConfig,
		},
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)
	reqBody := `{"model":"model1", "temperature":0.1, "x_param":"123", "y_param":"abc", "stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// `temperature` and `stream` are gone but model remains
	assert.Equal(t, `{"model":"model1", "x_param":"123", "y_param":"abc"}`, response["request_body"])

	// assert.Nil(t, response["temperature"])
	// assert.Equal(t, "123", response["x_param"])
	// assert.Equal(t, "abc", response["y_param"])
	// t.Logf("%v", response)
}

func TestProxyManager_MiddlewareWritesMetrics_NonStreaming(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// Make a non-streaming request
	reqBody := `{"model":"model1", "stream": false}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Check that metrics were recorded
	metrics := proxy.metricsMonitor.GetMetrics()
	if !assert.NotEmpty(t, metrics, "metrics should be recorded for non-streaming request") {
		return
	}

	// Verify the last metric has the correct model
	lastMetric := metrics[len(metrics)-1]
	assert.Equal(t, "model1", lastMetric.Model)
	assert.Equal(t, 25, lastMetric.InputTokens, "input tokens should be 25")
	assert.Equal(t, 10, lastMetric.OutputTokens, "output tokens should be 10")
	assert.Greater(t, lastMetric.TokensPerSecond, 0.0, "tokens per second should be greater than 0")
	assert.Greater(t, lastMetric.DurationMs, 0, "duration should be greater than 0")
}

func TestProxyManager_MiddlewareWritesMetrics_Streaming(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// Make a streaming request
	reqBody := `{"model":"model1", "stream": true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions?stream=true", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Check that metrics were recorded
	metrics := proxy.metricsMonitor.GetMetrics()
	if !assert.NotEmpty(t, metrics, "metrics should be recorded for streaming request") {
		return
	}

	// Verify the last metric has the correct model
	lastMetric := metrics[len(metrics)-1]
	assert.Equal(t, "model1", lastMetric.Model)
	assert.Equal(t, 25, lastMetric.InputTokens, "input tokens should be 25")
	assert.Equal(t, 10, lastMetric.OutputTokens, "output tokens should be 10")
	assert.Greater(t, lastMetric.TokensPerSecond, 0.0, "tokens per second should be greater than 0")
	assert.Greater(t, lastMetric.DurationMs, 0, "duration should be greater than 0")
}

func TestProxyManager_HealthEndpoint(t *testing.T) {
	config := AddDefaultGroupToConfig(Config{
		HealthCheckTimeout: 15,
		Models: map[string]ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OK", rec.Body.String())
}

func TestProxyManager_StartupHooks(t *testing.T) {

	// using real YAML as the configuration has gotten more complex
	// is the right approach as LoadConfigFromReader() does a lot more
	// than parse YAML now. Eventually migrate all tests to use this approach
	configStr := strings.Replace(`
logLevel: error
hooks:
  on_startup:
    preload:
      - model1
      - model2
groups:
  preloadTestGroup:
    swap: false
    members:
       - model1
       - model2
models:
  model1:
    cmd: ${simpleresponderpath} --port ${PORT} --silent --respond model1
  model2:
      cmd: ${simpleresponderpath} --port ${PORT} --silent --respond model2
`, "${simpleresponderpath}", simpleResponderPath, -1)

	// Create a test model configuration
	config, err := LoadConfigFromReader(strings.NewReader(configStr))
	if !assert.NoError(t, err, "Invalid configuration") {
		return
	}

	preloadChan := make(chan ModelPreloadedEvent, 2) // buffer for 2 expected events

	unsub := event.On(func(e ModelPreloadedEvent) {
		preloadChan <- e
	})

	defer unsub()

	// Create the proxy which should trigger preloading
	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	for i := 0; i < 2; i++ {
		select {
		case <-preloadChan:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for models to preload")
		}
	}
	// make sure they are both loaded
	_, foundGroup := proxy.processGroups["preloadTestGroup"]
	if !assert.True(t, foundGroup, "preloadTestGroup should exist") {
		return
	}
	assert.Equal(t, StateReady, proxy.processGroups["preloadTestGroup"].processes["model1"].CurrentState())
	assert.Equal(t, StateReady, proxy.processGroups["preloadTestGroup"].processes["model2"].CurrentState())
}
