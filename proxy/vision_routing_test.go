package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
)

func TestProxyManager_HasImageContent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			"text only - string content",
			`{"model":"test","messages":[{"role":"user","content":"hello"}]}`,
			false,
		},
		{
			"text only - array content",
			`{"model":"test","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
			false,
		},
		{
			"with image_url",
			`{"model":"test","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}}]}]}`,
			true,
		},
		{
			"empty messages",
			`{"model":"test","messages":[]}`,
			false,
		},
		{
			"no messages key",
			`{"model":"test"}`,
			false,
		},
		{
			"image in earlier message",
			`{"model":"test","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]},{"role":"assistant","content":"ok"},{"role":"user","content":"more?"}]}`,
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasImageContent([]byte(tt.body))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProxyManager_VisionModelRouting(t *testing.T) {
	textModel := getTestSimpleResponderConfig("text-response")
	visionModel := getTestSimpleResponderConfig("vision-response")
	textModel.VisionModel = "vision-model"

	conf := config.AddDefaultGroupToConfig(config.Config{
		HealthCheckTimeout: 15,
		LogLevel:           "error",
		Models: map[string]config.ModelConfig{
			"text-model":   textModel,
			"vision-model": visionModel,
		},
	})

	proxy := New(conf)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	t.Run("text request stays on text model", func(t *testing.T) {
		reqBody := `{"model":"text-model","messages":[{"role":"user","content":"hello"}]}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		// simple-responder echoes its --respond arg; text model responds with "text-response"
		assert.Equal(t, "text-response", response["responseMessage"])
	})

	t.Run("image request routes to vision model", func(t *testing.T) {
		reqBody := `{"model":"text-model","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}}]}]}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		// should have been routed to vision model which responds with "vision-response"
		assert.Equal(t, "vision-response", response["responseMessage"])
	})

	t.Run("image request on non-chat endpoint stays on text model", func(t *testing.T) {
		reqBody := `{"model":"text-model","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`
		req := httptest.NewRequest("POST", "/v1/completions", bytes.NewBufferString(reqBody))
		w := CreateTestResponseRecorder()

		proxy.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		// /v1/completions should NOT trigger vision routing
		assert.Equal(t, "text-response", response["responseMessage"])
	})
}
