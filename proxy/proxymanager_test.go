package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

// TestResponseRecorder adds CloseNotify to httptest.ResponseRecorder.
// "If you want to write your own tests around streams you will need a Recorder that can handle CloseNotifier."
// The tests can panic otherwise:
// panic: interface conversion: *httptest.ResponseRecorder is not http.CloseNotifier: missing method CloseNotify
// See: https://github.com/gin-gonic/gin/issues/1815
// TestResponseRecorder is taken from gin's own tests: https://github.com/gin-gonic/gin/blob/ce20f107f5dc498ec7489d7739541a25dcd48463/context_test.go#L1747-L1765
type TestResponseRecorder struct {
	*httptest.ResponseRecorder
	closeChannel chan bool
}

func (r *TestResponseRecorder) CloseNotify() <-chan bool {
	return r.closeChannel
}

func CreateTestResponseRecorder() *TestResponseRecorder {
	return &TestResponseRecorder{
		httptest.NewRecorder(),
		make(chan bool, 1),
	}
}

func TestProxyManager_SwapProcessCorrectly(t *testing.T) {
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
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
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), modelName)
	}
}
func TestProxyManager_SwapMultiProcess(t *testing.T) {
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		LogLevel: "error",
		Groups: map[string]config.GroupConfig{
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
			w := CreateTestResponseRecorder()

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
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"), // goes into the default group
			"model2": getTestSimpleResponderConfig("model2"),
		},
		LogLevel: "error",
		Groups: map[string]config.GroupConfig{
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
		w := CreateTestResponseRecorder()

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

	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
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
			w := CreateTestResponseRecorder()

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

	cfg := config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": model1Config,
			"model2": model2Config,
			"model3": getTestSimpleResponderConfig("model3"),
		},
		Peers: map[string]config.PeerConfig{
			"peer1": {
				Proxy:  "http://peer1:8080",
				Models: []string{"peer-model-a", "peer-model-b"},
			},
		},
		LogLevel: "error",
	}

	proxy := New(cfg)

	// Create a test request
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Add("Origin", "i-am-the-origin")
	w := CreateTestResponseRecorder()

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

	// Check the number of models returned (3 local + 2 peer models)
	assert.Len(t, response.Data, 5)

	// Check the details of each model
	expectedModels := map[string]struct{}{
		"model1":       {},
		"model2":       {},
		"model3":       {},
		"peer-model-a": {},
		"peer-model-b": {},
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
		} else if modelID == "peer-model-a" || modelID == "peer-model-b" {
			// Peer models should have meta.llamaswap.peerID
			meta, exists := model["meta"]
			assert.True(t, exists, "peer model should have meta field")
			metaMap, ok := meta.(map[string]interface{})
			assert.True(t, ok, "meta should be a map")
			llamaswap, exists := metaMap["llamaswap"]
			assert.True(t, exists, "meta should have llamaswap field")
			llamaswapMap, ok := llamaswap.(map[string]interface{})
			assert.True(t, ok, "llamaswap should be a map")
			peerID, exists := llamaswapMap["peerID"]
			assert.True(t, exists, "llamaswap should have peerID field")
			assert.Equal(t, "peer1", peerID)
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

func TestProxyManager_ListModelsHandler_WithMetadata(t *testing.T) {
	// Process config through LoadConfigFromReader to apply macro substitution
	configYaml := `
healthCheckTimeout: 15
logLevel: error
startPort: 10000
models:
  model1:
    cmd: /path/to/server -p ${PORT}
    macros:
      PORT_NUM: 10001
      TEMP: 0.7
      NAME: "llama"
    metadata:
      port: ${PORT_NUM}
      temperature: ${TEMP}
      enabled: true
      note: "Running on port ${PORT_NUM}"
      nested:
        value: ${TEMP}
  model2:
    cmd: /path/to/server -p ${PORT}
`
	processedConfig, err := config.LoadConfigFromReader(strings.NewReader(configYaml))
	assert.NoError(t, err)

	proxy := New(processedConfig)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := CreateTestResponseRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Data []map[string]any `json:"data"`
	}

	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, response.Data, 2)

	// Find model1 and model2 in response
	var model1Data, model2Data map[string]any
	for _, model := range response.Data {
		if model["id"] == "model1" {
			model1Data = model
		} else if model["id"] == "model2" {
			model2Data = model
		}
	}

	// Verify model1 has llamaswap_meta
	assert.NotNil(t, model1Data)
	meta, exists := model1Data["meta"]
	if !assert.True(t, exists, "model1 should have meta key") {
		t.FailNow()
	}

	metaMap := meta.(map[string]any)

	lsmeta, exists := metaMap["llamaswap"]
	if !assert.True(t, exists, "model1 should have meta.llamaswap key") {
		t.FailNow()
	}

	lsmetamap := lsmeta.(map[string]any)

	// Verify type preservation
	assert.Equal(t, float64(10001), lsmetamap["port"]) // JSON numbers are float64
	assert.Equal(t, 0.7, lsmetamap["temperature"])
	assert.Equal(t, true, lsmetamap["enabled"])
	// Verify string interpolation
	assert.Equal(t, "Running on port 10001", lsmetamap["note"])
	// Verify nested structure
	nested := lsmetamap["nested"].(map[string]any)
	assert.Equal(t, 0.7, nested["value"])

	// Verify model2 does NOT have llamaswap_meta
	assert.NotNil(t, model2Data)
	_, exists = model2Data["llamaswap_meta"]
	assert.False(t, exists, "model2 should not have llamaswap_meta")
}

func TestProxyManager_ListModelsHandler_SortedByID(t *testing.T) {
	// Intentionally add models in non-sorted order and with an unlisted model
	config := config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"zeta":  getTestSimpleResponderConfig("zeta"),
			"alpha": getTestSimpleResponderConfig("alpha"),
			"beta":  getTestSimpleResponderConfig("beta"),
			"hidden": func() config.ModelConfig {
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
	w := CreateTestResponseRecorder()
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

func TestProxyManager_ListModelsHandler_IncludeAliasesInList(t *testing.T) {
	// Configure alias
	config := config.Config{
		HealthCheckTimeout:   15,
		IncludeAliasesInList: true,
		Models: map[string]config.ModelConfig{
			"model1": func() config.ModelConfig {
				mc := getTestSimpleResponderConfig("model1")
				mc.Name = "Model 1"
				mc.Aliases = []string{"alias1"}
				return mc
			}(),
		},
		LogLevel: "error",
	}

	proxy := New(config)

	// Request models list
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := CreateTestResponseRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	// We expect both base id and alias
	var model1Data, alias1Data map[string]any
	for _, model := range response.Data {
		if model["id"] == "model1" {
			model1Data = model
		} else if model["id"] == "alias1" {
			alias1Data = model
		}
	}

	// Verify model1 has name
	assert.NotNil(t, model1Data)
	_, exists := model1Data["name"]
	if !assert.True(t, exists, "model1 should have name key") {
		t.FailNow()
	}
	name1, ok := model1Data["name"].(string)
	assert.True(t, ok, "name1 should be a string")

	// Verify alias1 has name
	assert.NotNil(t, alias1Data)
	_, exists = alias1Data["name"]
	if !assert.True(t, exists, "alias1 should have name key") {
		t.FailNow()
	}
	name2, ok := alias1Data["name"].(string)
	assert.True(t, ok, "name2 should be a string")

	// Name keys should match
	assert.Equal(t, name1, name2)
}

func TestProxyManager_Shutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	// make broken model configurations
	model1Config := getTestSimpleResponderConfigPort("model1", 9991)
	model1Config.Proxy = "http://localhost:10001/"

	model2Config := getTestSimpleResponderConfigPort("model2", 9992)
	model2Config.Proxy = "http://localhost:10002/"

	model3Config := getTestSimpleResponderConfigPort("model3", 9993)
	model3Config.Proxy = "http://localhost:10003/"

	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": model1Config,
			"model2": model2Config,
			"model3": model3Config,
		},
		LogLevel: "error",
		Groups: map[string]config.GroupConfig{
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
			w := CreateTestResponseRecorder()

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
	conf := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(conf)
	reqBody := fmt.Sprintf(`{"model":"%s"}`, "model1")
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, proxy.processGroups[config.DEFAULT_GROUP_ID].processes["model1"].CurrentState(), StateReady)
	req = httptest.NewRequest("GET", "/unload", nil)
	w = CreateTestResponseRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, w.Body.String(), "OK")

	select {
	case <-proxy.processGroups[config.DEFAULT_GROUP_ID].processes["model1"].cmdWaitChan:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for model1 to stop")
	}
	assert.Equal(t, proxy.processGroups[config.DEFAULT_GROUP_ID].processes["model1"].CurrentState(), StateStopped)
}

func TestProxyManager_UnloadSingleModel(t *testing.T) {
	const testGroupId = "testGroup"
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		Groups: map[string]config.GroupConfig{
			testGroupId: {
				Swap:    false,
				Members: []string{"model1", "model2"},
			},
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopImmediately)

	// start both model
	for _, modelName := range []string{"model1", "model2"} {
		reqBody := fmt.Sprintf(`{"model":"%s"}`, modelName)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()
		proxy.ServeHTTP(w, req)
	}

	assert.Equal(t, StateReady, proxy.processGroups[testGroupId].processes["model1"].CurrentState())
	assert.Equal(t, StateReady, proxy.processGroups[testGroupId].processes["model2"].CurrentState())

	req := httptest.NewRequest("POST", "/api/models/unload/model1", nil)
	w := CreateTestResponseRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	if !assert.Equal(t, w.Body.String(), "OK") {
		t.FailNow()
	}

	select {
	case <-proxy.processGroups[testGroupId].processes["model1"].cmdWaitChan:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for model1 to stop")
	}

	assert.Equal(t, proxy.processGroups[testGroupId].processes["model1"].CurrentState(), StateStopped)
	assert.Equal(t, proxy.processGroups[testGroupId].processes["model2"].CurrentState(), StateReady)
}

// Test issue #61 `Listing the current list of models and the loaded model.`
func TestProxyManager_RunningEndpoint(t *testing.T) {
	// Shared configuration
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		LogLevel: "warn",
	})

	// Define a helper struct to parse the JSON response.
	type RunningResponse struct {
		Running []struct {
			Model       string `json:"model"`
			State       string `json:"state"`
			Cmd         string `json:"cmd"`
			Proxy       string `json:"proxy"`
			TTL         int    `json:"ttl"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"running"`
	}

	// Create proxy once for all tests
	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	t.Run("no models loaded", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/running", nil)
		w := CreateTestResponseRecorder()
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
		w := CreateTestResponseRecorder()
		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		// Simulate browser call for the `/running` endpoint.
		req = httptest.NewRequest("GET", "/running", nil)
		w = CreateTestResponseRecorder()
		proxy.ServeHTTP(w, req)

		var response RunningResponse
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

		// Check if we have a single array element.
		assert.Len(t, response.Running, 1)

		// Is this the right model?
		assert.Equal(t, "model1", response.Running[0].Model)

		// Is the model loaded?
		assert.Equal(t, "ready", response.Running[0].State)

		// Verify extended fields are present
		assert.NotEmpty(t, response.Running[0].Cmd, "cmd should be populated")
		assert.NotEmpty(t, response.Running[0].Proxy, "proxy should be populated")
		assert.Equal(t, 0, response.Running[0].TTL, "ttl should default to 0")
	})
}

func TestProxyManager_AudioTranscriptionHandler(t *testing.T) {
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
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
	rec := CreateTestResponseRecorder()
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

	conf := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": modelConfig,
		},
		LogLevel: "error",
	})

	proxy := New(conf)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	requestedModel := "model1"

	t.Run("useModelName over rides requested model: /v1/chat/completions", func(t *testing.T) {
		reqBody := fmt.Sprintf(`{"model":"%s"}`, requestedModel)
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

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
		rec := CreateTestResponseRecorder()
		proxy.ServeHTTP(rec, req)

		// Verify the response
		assert.Equal(t, http.StatusOK, rec.Code)
		var response map[string]string
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Equal(t, upstreamModelName, response["model"])
	})
}

func TestProxyManager_AudioVoicesGETHandler(t *testing.T) {
	conf := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(conf)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	t.Run("successful GET with model query param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/audio/voices?model=model1", nil)
		w := CreateTestResponseRecorder()
		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "voice1")
	})

	t.Run("missing model query param returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/audio/voices", nil)
		w := CreateTestResponseRecorder()
		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "missing required 'model' query parameter")
	})

	t.Run("unknown model returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/audio/voices?model=nonexistent", nil)
		w := CreateTestResponseRecorder()
		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "could not find suitable handler")
	})
}

func TestProxyManager_CORSOptionsHandler(t *testing.T) {
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
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

			w := CreateTestResponseRecorder()
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

	config, err := config.LoadConfigFromReader(strings.NewReader(configStr))
	assert.NoError(t, err)

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)
	t.Run("main model name", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/upstream/model1/test", nil)
		rec := CreateTestResponseRecorder()
		proxy.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "model1", rec.Body.String())
	})

	t.Run("model alias", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/upstream/model-alias/test", nil)
		rec := CreateTestResponseRecorder()
		proxy.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "model1", rec.Body.String())
	})
}

func TestProxyManager_ChatContentLength(t *testing.T) {
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	reqBody := fmt.Sprintf(`{"model":"%s", "x": "this is just some content to push the length out a bit"}`, "model1")
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "81", response["h_content_length"])
	assert.Equal(t, "model1", response["responseMessage"])
}

func TestProxyManager_FiltersStripParams(t *testing.T) {
	modelConfig := getTestSimpleResponderConfig("model1")
	modelConfig.Filters = config.ModelFilters{
		Filters: config.Filters{
			StripParams: "temperature, model, stream",
		},
	}

	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		LogLevel:           "error",
		Models: map[string]config.ModelConfig{
			"model1": modelConfig,
		},
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)
	reqBody := `{"model":"model1", "temperature":0.1, "x_param":"123", "y_param":"abc", "stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

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

func TestProxyManager_HealthEndpoint(t *testing.T) {
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)
	req := httptest.NewRequest("GET", "/health", nil)
	rec := CreateTestResponseRecorder()
	proxy.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OK", rec.Body.String())
}

// Ensure the custom llama-server /completion endpoint proxies correctly
func TestProxyManager_CompletionEndpoint(t *testing.T) {
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	reqBody := `{"model":"model1"}`
	req := httptest.NewRequest("POST", "/completion", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "model1")
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
	config, err := config.LoadConfigFromReader(strings.NewReader(configStr))
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

func TestProxyManager_StreamingEndpointsReturnNoBufferingHeader(t *testing.T) {
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1":       getTestSimpleResponderConfig("model1"),
			"author/model": getTestSimpleResponderConfig("author/model"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	endpoints := []string{
		"/api/events",
		"/logs/stream",
		"/logs/stream/proxy",
		"/logs/stream/upstream",
		"/logs/stream/author/model",
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			req := httptest.NewRequest("GET", endpoint, nil)
			req = req.WithContext(ctx)
			rec := CreateTestResponseRecorder()

			// Run handler in goroutine and wait for context timeout
			done := make(chan struct{})
			go func() {
				defer close(done)
				proxy.ServeHTTP(rec, req)
			}()

			// Wait for either the handler to complete or context to timeout
			<-ctx.Done()

			// At this point, the handler has either finished or been cancelled
			// Wait for the goroutine to fully exit before reading
			<-done

			// Now it's safe to read from rec - no more concurrent writes
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"))
		})
	}
}

func TestProxyManager_ProxiedStreamingEndpointReturnsNoBufferingHeader(t *testing.T) {
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"streaming-model": getTestSimpleResponderConfig("streaming-model"),
		},
		LogLevel: "error",
	})

	proxy := New(config)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// Make a streaming request
	reqBody := `{"model":"streaming-model"}`
	// simple-responder will return text/event-stream when stream=true is in the query
	req := httptest.NewRequest("POST", "/v1/chat/completions?stream=true", bytes.NewBufferString(reqBody))
	rec := CreateTestResponseRecorder()

	proxy.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"))
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
}

func TestProxyManager_ApiGetVersion(t *testing.T) {
	config := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	// Version test map
	versionTest := map[string]string{
		"build_date": "1970-01-01T00:00:00Z",
		"commit":     "cc915ddb6f04a42d9cd1f524e1d46ec6ed069fdc",
		"version":    "v001",
	}

	proxy := New(config)
	proxy.SetVersion(versionTest["build_date"], versionTest["commit"], versionTest["version"])
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	req := httptest.NewRequest("GET", "/api/version", nil)
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Ensure json response
	assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))

	// Check for attributes
	response := map[string]string{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	for key, value := range versionTest {
		assert.Equal(t, value, response[key], "%s value %s should match response %s", key, value, response[key])
	}
}

func TestProxyManager_APIKeyAuth(t *testing.T) {
	testConfig := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		RequiredAPIKeys: []string{"valid-key-1", "valid-key-2"},
		LogLevel:        "error",
	})

	proxy := New(testConfig)
	defer proxy.StopProcesses(StopImmediately)

	t.Run("valid key in x-api-key header", func(t *testing.T) {
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		req.Header.Set("x-api-key", "valid-key-1")
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("valid key in Authorization Bearer header", func(t *testing.T) {
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		req.Header.Set("Authorization", "Bearer valid-key-2")
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("both headers with matching keys", func(t *testing.T) {
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		req.Header.Set("x-api-key", "valid-key-1")
		req.Header.Set("Authorization", "Bearer valid-key-1")
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("invalid key returns 401", func(t *testing.T) {
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		req.Header.Set("x-api-key", "invalid-key")
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "unauthorized")
	})

	t.Run("missing key returns 401", func(t *testing.T) {
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("valid key in Basic Auth header", func(t *testing.T) {
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		// Basic Auth: base64("anyuser:valid-key-1")
		credentials := base64.StdEncoding.EncodeToString([]byte("anyuser:valid-key-1"))
		req.Header.Set("Authorization", "Basic "+credentials)
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("invalid key in Basic Auth header returns 401", func(t *testing.T) {
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		credentials := base64.StdEncoding.EncodeToString([]byte("anyuser:wrong-key"))
		req.Header.Set("Authorization", "Basic "+credentials)
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "unauthorized")
	})

	t.Run("x-api-key and Basic Auth with matching keys", func(t *testing.T) {
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		req.Header.Set("x-api-key", "valid-key-1")
		credentials := base64.StdEncoding.EncodeToString([]byte("user:valid-key-1"))
		req.Header.Set("Authorization", "Basic "+credentials)
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("401 response includes WWW-Authenticate header", func(t *testing.T) {
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, `Basic realm="llama-swap"`, w.Header().Get("WWW-Authenticate"))
	})
}

func TestProxyManager_APIKeyAuth_Disabled(t *testing.T) {
	// Config without RequiredAPIKeys - auth should be disabled
	testConfig := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})

	proxy := New(testConfig)
	defer proxy.StopProcesses(StopImmediately)

	t.Run("requests pass without API key when not configured", func(t *testing.T) {
		reqBody := `{"model":"model1"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// TestProxyManager_PeerProxy_InferenceHandler tests the peerProxy integration
// in proxyInferenceHandler for issue #433
func TestProxyManager_PeerProxy_InferenceHandler(t *testing.T) {
	t.Run("requests to peer models are proxied", func(t *testing.T) {
		// Create a test server to act as the peer
		peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"response":"from-peer","model":"peer-model"}`))
		}))
		defer peerServer.Close()

		// Create config with peers but no local model for "peer-model"
		configStr := fmt.Sprintf(`
logLevel: error
peers:
  test-peer:
    proxy: %s
    models:
      - peer-model
models:
  local-model:
    cmd: %s -port ${PORT} -silent -respond local-model
`, peerServer.URL, getSimpleResponderPath())

		testConfig, err := config.LoadConfigFromReader(strings.NewReader(configStr))
		assert.NoError(t, err)

		proxy := New(testConfig)
		defer proxy.StopProcesses(StopImmediately)

		reqBody := `{"model":"peer-model"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "from-peer")
	})

	t.Run("local models take precedence over peer models", func(t *testing.T) {
		// Create a test server to act as the peer - should NOT be called
		peerCalled := false
		peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peerCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"response":"from-peer"}`))
		}))
		defer peerServer.Close()

		// Create config where "shared-model" exists both locally and on peer
		configStr := fmt.Sprintf(`
logLevel: error
peers:
  test-peer:
    proxy: %s
    models:
      - shared-model
models:
  shared-model:
    cmd: %s -port ${PORT} -silent -respond local-response
`, peerServer.URL, getSimpleResponderPath())

		testConfig, err := config.LoadConfigFromReader(strings.NewReader(configStr))
		assert.NoError(t, err)

		proxy := New(testConfig)
		defer proxy.StopProcesses(StopImmediately)

		reqBody := `{"model":"shared-model"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "local-response")
		assert.False(t, peerCalled, "peer should not be called when local model exists")
	})

	t.Run("unknown model returns error", func(t *testing.T) {
		// Create a test server to act as the peer
		peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer peerServer.Close()

		configStr := fmt.Sprintf(`
logLevel: error
peers:
  test-peer:
    proxy: %s
    models:
      - peer-model
models:
  local-model:
    cmd: %s -port ${PORT} -silent -respond local-model
`, peerServer.URL, getSimpleResponderPath())

		testConfig, err := config.LoadConfigFromReader(strings.NewReader(configStr))
		assert.NoError(t, err)

		proxy := New(testConfig)
		defer proxy.StopProcesses(StopImmediately)

		reqBody := `{"model":"unknown-model"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "could not find suitable inference handler")
	})

	t.Run("peer API key is injected into request", func(t *testing.T) {
		var receivedAuthHeader string
		peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuthHeader = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"response":"ok"}`))
		}))
		defer peerServer.Close()

		configStr := fmt.Sprintf(`
logLevel: error
peers:
  test-peer:
    proxy: %s
    apiKey: secret-peer-key
    models:
      - peer-model
models:
  local-model:
    cmd: %s -port ${PORT} -silent -respond local-model
`, peerServer.URL, getSimpleResponderPath())

		testConfig, err := config.LoadConfigFromReader(strings.NewReader(configStr))
		assert.NoError(t, err)

		proxy := New(testConfig)
		defer proxy.StopProcesses(StopImmediately)

		reqBody := `{"model":"peer-model"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "Bearer secret-peer-key", receivedAuthHeader)
	})

	t.Run("no peers configured - unknown model returns error", func(t *testing.T) {
		testConfig := config.AddDefaultGroupToConfig(config.Config{
			HealthCheckTimeout: 15,
			Models: map[string]config.ModelConfig{
				"local-model": getTestSimpleResponderConfig("local-model"),
			},
			LogLevel: "error",
		})

		proxy := New(testConfig)
		defer proxy.StopProcesses(StopImmediately)

		// peerProxy exists but has no peer models configured
		assert.False(t, proxy.peerProxy.HasPeerModel("unknown-model"))

		reqBody := `{"model":"unknown-model"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "could not find suitable inference handler")
	})

	t.Run("peer streaming response sets X-Accel-Buffering header", func(t *testing.T) {
		peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("data: test\n\n"))
		}))
		defer peerServer.Close()

		configStr := fmt.Sprintf(`
logLevel: error
peers:
  test-peer:
    proxy: %s
    models:
      - peer-model
models:
  local-model:
    cmd: %s -port ${PORT} -silent -respond local-model
`, peerServer.URL, getSimpleResponderPath())

		testConfig, err := config.LoadConfigFromReader(strings.NewReader(configStr))
		assert.NoError(t, err)

		proxy := New(testConfig)
		defer proxy.StopProcesses(StopImmediately)

		reqBody := `{"model":"peer-model"}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "no", w.Header().Get("X-Accel-Buffering"))
	})
}
