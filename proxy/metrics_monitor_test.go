package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
	"github.com/mostlygeek/llama-swap/proxy/cache"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

func TestMetricsMonitor_AddMetrics(t *testing.T) {
	t.Run("adds metrics and assigns ID", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		metric := ActivityLogEntry{
			Model: "test-model",
			Tokens: TokenMetrics{
				InputTokens:  100,
				OutputTokens: 50,
			},
		}

		mm.queueMetrics(metric)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, 0, metrics[0].ID)
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 100, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 50, metrics[0].Tokens.OutputTokens)
	})

	t.Run("increments ID for each metric", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		for i := 0; i < 5; i++ {
			mm.queueMetrics(ActivityLogEntry{Model: "model"})
		}

		metrics := mm.getMetrics()
		assert.Equal(t, 5, len(metrics))
		for i := 0; i < 5; i++ {
			assert.Equal(t, i, metrics[i].ID)
		}
	})

	t.Run("respects max metrics limit", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 3, 0)

		// Add 5 metrics
		for i := 0; i < 5; i++ {
			mm.queueMetrics(ActivityLogEntry{
				Model: "model",
				Tokens: TokenMetrics{
					InputTokens: i,
				},
			})
		}

		metrics := mm.getMetrics()
		assert.Equal(t, 3, len(metrics))

		// Should keep the last 3 metrics (IDs 2, 3, 4)
		assert.Equal(t, 2, metrics[0].ID)
		assert.Equal(t, 3, metrics[1].ID)
		assert.Equal(t, 4, metrics[2].ID)
	})

	t.Run("emits ActivityLogEvent", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		receivedEvent := make(chan ActivityLogEvent, 1)
		cancel := event.On(func(e ActivityLogEvent) {
			receivedEvent <- e
		})
		defer cancel()

		metric := ActivityLogEntry{
			Model: "test-model",
			Tokens: TokenMetrics{
				InputTokens:  100,
				OutputTokens: 50,
			},
		}

		mm.queueMetrics(metric)
		mm.emitMetric(metric)

		select {
		case evt := <-receivedEvent:
			assert.Equal(t, 0, evt.Metrics.ID)
			assert.Equal(t, "test-model", evt.Metrics.Model)
			assert.Equal(t, 100, evt.Metrics.Tokens.InputTokens)
			assert.Equal(t, 50, evt.Metrics.Tokens.OutputTokens)
		case <-time.After(1 * time.Second):
			t.Fatal("timeout waiting for event")
		}
	})
}

func TestMetricsMonitor_GetMetrics(t *testing.T) {
	t.Run("returns empty slice when no metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)
		metrics := mm.getMetrics()
		assert.NotNil(t, metrics)
		assert.Equal(t, 0, len(metrics))
	})

	t.Run("returns copy of metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)
		mm.queueMetrics(ActivityLogEntry{Model: "model1"})
		mm.queueMetrics(ActivityLogEntry{Model: "model2"})

		metrics1 := mm.getMetrics()
		metrics2 := mm.getMetrics()

		// Verify we got copies
		assert.Equal(t, 2, len(metrics1))
		assert.Equal(t, 2, len(metrics2))

		// Modify the returned slice shouldn't affect the original
		metrics1[0].Model = "modified"
		metrics3 := mm.getMetrics()
		assert.Equal(t, "model1", metrics3[0].Model)
	})
}

func TestMetricsMonitor_GetMetricsJSON(t *testing.T) {
	t.Run("returns valid JSON for empty metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)
		jsonData, err := mm.getMetricsJSON()
		assert.NoError(t, err)
		assert.NotNil(t, jsonData)

		var metrics []ActivityLogEntry
		err = json.Unmarshal(jsonData, &metrics)
		assert.NoError(t, err)
		assert.Equal(t, 0, len(metrics))
	})

	t.Run("returns valid JSON with metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)
		mm.queueMetrics(ActivityLogEntry{
			Model: "model1",
			Tokens: TokenMetrics{
				InputTokens:     100,
				OutputTokens:    50,
				TokensPerSecond: 25.5,
			},
		})
		mm.queueMetrics(ActivityLogEntry{
			Model: "model2",
			Tokens: TokenMetrics{
				InputTokens:     200,
				OutputTokens:    100,
				TokensPerSecond: 30.0,
			},
		})

		jsonData, err := mm.getMetricsJSON()
		assert.NoError(t, err)

		var metrics []ActivityLogEntry
		err = json.Unmarshal(jsonData, &metrics)
		assert.NoError(t, err)
		assert.Equal(t, 2, len(metrics))
		assert.Equal(t, "model1", metrics[0].Model)
		assert.Equal(t, "model2", metrics[1].Model)
	})
}

func TestMetricsMonitor_WrapHandler(t *testing.T) {
	t.Run("successful non-streaming request with usage data", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		responseBody := `{
			"usage": {
				"prompt_tokens": 100,
				"completion_tokens": 50
			}
		}`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 100, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 50, metrics[0].Tokens.OutputTokens)
	})

	t.Run("successful request with timings data", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		responseBody := `{
			"timings": {
				"prompt_n": 100,
				"predicted_n": 50,
				"prompt_per_second": 150.5,
				"predicted_per_second": 25.5,
				"prompt_ms": 500.0,
				"predicted_ms": 1500.0,
				"cache_n": 20
			}
		}`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 100, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 50, metrics[0].Tokens.OutputTokens)
		assert.Equal(t, 20, metrics[0].Tokens.CachedTokens)
		assert.Equal(t, 150.5, metrics[0].Tokens.PromptPerSecond)
		assert.Equal(t, 25.5, metrics[0].Tokens.TokensPerSecond)
		assert.Equal(t, 2000, metrics[0].DurationMs) // 500 + 1500
	})

	t.Run("streaming request with SSE format", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		// Note: SSE format requires proper line breaks - each data line followed by blank line
		responseBody := `data: {"choices":[{"text":"Hello"}]}

data: {"choices":[{"text":" World"}]}

data: {"usage":{"prompt_tokens":10,"completion_tokens":20},"timings":{"prompt_n":10,"predicted_n":20,"prompt_per_second":100.0,"predicted_per_second":50.0,"prompt_ms":100.0,"predicted_ms":400.0}}

data: [DONE]

`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		// When timings data is present, it takes precedence
		assert.Equal(t, 10, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 20, metrics[0].Tokens.OutputTokens)
	})

	t.Run("non-OK status code records partial metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("error"))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, "/test", metrics[0].ReqPath)
		assert.Equal(t, http.StatusBadRequest, metrics[0].RespStatusCode)
		assert.Equal(t, 0, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 0, metrics[0].Tokens.OutputTokens)
	})

	t.Run("empty response body records minimal metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.WriteHeader(http.StatusOK)
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 0, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 0, metrics[0].Tokens.OutputTokens)
	})

	t.Run("invalid JSON records minimal metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("not valid json"))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err) // Errors after response is sent are logged, not returned

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 0, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 0, metrics[0].Tokens.OutputTokens)
	})

	t.Run("next handler error is propagated", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		expectedErr := assert.AnError
		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			return expectedErr
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.Equal(t, expectedErr, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 0, len(metrics))
	})

	t.Run("response without usage or timings records minimal metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		responseBody := `{"result": "ok"}`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 0, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 0, metrics[0].Tokens.OutputTokens)
	})

	t.Run("infill request extracts timings from last array element", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		// Infill response is an array with timings in the last element
		responseBody := `[
			{"content": "first chunk"},
			{"content": "second chunk"},
			{"content": "final", "timings": {
				"prompt_n": 150,
				"predicted_n": 75,
				"prompt_per_second": 200.5,
				"predicted_per_second": 35.5,
				"prompt_ms": 600.0,
				"predicted_ms": 1800.0,
				"cache_n": 30
			}}
		]`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/infill", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 150, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 75, metrics[0].Tokens.OutputTokens)
		assert.Equal(t, 30, metrics[0].Tokens.CachedTokens)
		assert.Equal(t, 200.5, metrics[0].Tokens.PromptPerSecond)
		assert.Equal(t, 35.5, metrics[0].Tokens.TokensPerSecond)
		assert.Equal(t, 2400, metrics[0].DurationMs) // 600 + 1800
	})

	t.Run("infill request with empty array records minimal metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		responseBody := `[]`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/infill", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 0, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 0, metrics[0].Tokens.OutputTokens)
	})
}

func TestMetricsMonitor_ResponseBodyCopier(t *testing.T) {
	t.Run("captures response body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		copier := newBodyCopier(ginCtx.Writer)

		testData := []byte("test response body")
		n, err := copier.Write(testData)

		assert.NoError(t, err)
		assert.Equal(t, len(testData), n)
		assert.Equal(t, testData, copier.body.Bytes())
		assert.Equal(t, string(testData), rec.Body.String())
	})

	t.Run("sets start time on creation", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		copier := newBodyCopier(ginCtx.Writer)

		assert.False(t, copier.StartTime().IsZero())
	})

	t.Run("preserves headers", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		copier := newBodyCopier(ginCtx.Writer)

		copier.Header().Set("X-Test", "value")

		assert.Equal(t, "value", rec.Header().Get("X-Test"))
	})

	t.Run("preserves status code", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		copier := newBodyCopier(ginCtx.Writer)

		copier.WriteHeader(http.StatusCreated)

		// Gin's ResponseWriter tracks status internally
		assert.Equal(t, http.StatusCreated, copier.Status())
	})
}

func TestMetricsMonitor_Concurrent(t *testing.T) {
	t.Run("concurrent queueMetrics is safe", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 1000, 0)

		var wg sync.WaitGroup
		numGoroutines := 10
		metricsPerGoroutine := 100

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < metricsPerGoroutine; j++ {
					mm.queueMetrics(ActivityLogEntry{
						Model: "test-model",
						Tokens: TokenMetrics{
							InputTokens:  id*1000 + j,
							OutputTokens: j,
						},
					})
				}
			}(i)
		}

		wg.Wait()

		metrics := mm.getMetrics()
		assert.Equal(t, numGoroutines*metricsPerGoroutine, len(metrics))
	})

	t.Run("concurrent reads and writes are safe", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 100, 0)

		done := make(chan bool)

		// Writer goroutine
		go func() {
			for i := 0; i < 50; i++ {
				mm.queueMetrics(ActivityLogEntry{Model: "test-model"})
				time.Sleep(1 * time.Millisecond)
			}
			done <- true
		}()

		// Multiple reader goroutines
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					_ = mm.getMetrics()
					_, _ = mm.getMetricsJSON()
					time.Sleep(2 * time.Millisecond)
				}
			}()
		}

		<-done
		wg.Wait()

		// Final check
		metrics := mm.getMetrics()
		assert.Equal(t, 50, len(metrics))
	})
}

func TestMetricsMonitor_ParseMetrics(t *testing.T) {
	t.Run("keeps wall clock duration when timings underreport request time", func(t *testing.T) {
		start := time.Now().Add(-5 * time.Second)
		usage := gjson.Parse(`{"prompt_tokens": 5, "completion_tokens": 1}`)
		timings := gjson.Parse(`{
			"prompt_n": 5,
			"predicted_n": 1,
			"prompt_per_second": 10.0,
			"predicted_per_second": 2.0,
			"prompt_ms": 5.0,
			"predicted_ms": 15.0
		}`)

		metrics, err := parseMetrics("test-model", start, usage, timings)
		assert.NoError(t, err)
		assert.Equal(t, 5, metrics.Tokens.InputTokens)
		assert.Equal(t, 1, metrics.Tokens.OutputTokens)
		assert.Equal(t, 10.0, metrics.Tokens.PromptPerSecond)
		assert.Equal(t, 2.0, metrics.Tokens.TokensPerSecond)
		assert.GreaterOrEqual(t, metrics.DurationMs, 5000)
	})

	t.Run("prefers timings over usage data", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		// Timings should take precedence over usage
		responseBody := `{
			"usage": {
				"prompt_tokens": 50,
				"completion_tokens": 25
			},
			"timings": {
				"prompt_n": 100,
				"predicted_n": 50,
				"prompt_per_second": 150.5,
				"predicted_per_second": 25.5,
				"prompt_ms": 500.0,
				"predicted_ms": 1500.0
			}
		}`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		// Should use timings values, not usage values
		assert.Equal(t, 100, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 50, metrics[0].Tokens.OutputTokens)
	})

	t.Run("handles missing cache_n in timings", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		responseBody := `{
			"timings": {
				"prompt_n": 100,
				"predicted_n": 50,
				"prompt_per_second": 150.5,
				"predicted_per_second": 25.5,
				"prompt_ms": 500.0,
				"predicted_ms": 1500.0
			}
		}`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, -1, metrics[0].Tokens.CachedTokens) // Default value when not present
	})
}

func TestMetricsMonitor_StreamingResponse(t *testing.T) {
	t.Run("finds metrics in last valid SSE data", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		// Metrics should be found in the last data line before [DONE]
		responseBody := `data: {"choices":[{"text":"First"}]}

data: {"choices":[{"text":"Second"}]}

data: {"usage":{"prompt_tokens":100,"completion_tokens":50}}

data: [DONE]

`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, 100, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 50, metrics[0].Tokens.OutputTokens)
	})

	t.Run("handles streaming with no valid JSON records minimal metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		responseBody := `data: not json

data: [DONE]

`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 0, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 0, metrics[0].Tokens.OutputTokens)
	})

	t.Run("v1/responses format with nested response.usage", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		// v1/responses SSE format: usage is nested under response.usage
		responseBody := "event: response.completed\n" +
			`data: {"type":"response.completed","response":{"id":"resp_abc","object":"response","created_at":1773416985,"status":"completed","model":"test-model","output":[],"usage":{"input_tokens":17,"output_tokens":23,"total_tokens":40}}}` +
			"\n\n"

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/v1/responses", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 17, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 23, metrics[0].Tokens.OutputTokens)
	})

	t.Run("handles empty streaming response records minimal metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		responseBody := ``

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 0, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 0, metrics[0].Tokens.OutputTokens)
	})
}

// Benchmark tests
func BenchmarkMetricsMonitor_AddMetrics(b *testing.B) {
	mm := newMetricsMonitor(testLogger, 1000, 0)

	metric := ActivityLogEntry{
		Model: "test-model",
		Tokens: TokenMetrics{
			CachedTokens:    100,
			InputTokens:     500,
			OutputTokens:    250,
			PromptPerSecond: 1200.5,
			TokensPerSecond: 45.8,
		},
		DurationMs: 5000,
		Timestamp:  time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mm.queueMetrics(metric)
	}
}

func BenchmarkMetricsMonitor_AddMetrics_SmallBuffer(b *testing.B) {
	// Test performance with a smaller buffer where wrapping occurs more frequently
	mm := newMetricsMonitor(testLogger, 100, 0)

	metric := ActivityLogEntry{
		Model: "test-model",
		Tokens: TokenMetrics{
			CachedTokens:    100,
			InputTokens:     500,
			OutputTokens:    250,
			PromptPerSecond: 1200.5,
			TokensPerSecond: 45.8,
		},
		DurationMs: 5000,
		Timestamp:  time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mm.queueMetrics(metric)
	}
}

func TestMetricsMonitor_WrapHandler_Compression(t *testing.T) {
	t.Run("gzip encoded response", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		responseBody := `{"usage": {"prompt_tokens": 100, "completion_tokens": 50}}`

		// Compress with gzip
		var buf bytes.Buffer
		gzWriter := gzip.NewWriter(&buf)
		gzWriter.Write([]byte(responseBody))
		gzWriter.Close()
		compressedBody := buf.Bytes()

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusOK)
			w.Write(compressedBody)
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 100, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 50, metrics[0].Tokens.OutputTokens)
	})

	t.Run("deflate encoded response", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		responseBody := `{"usage": {"prompt_tokens": 200, "completion_tokens": 75}}`

		// Compress with deflate
		var buf bytes.Buffer
		flateWriter, _ := flate.NewWriter(&buf, flate.DefaultCompression)
		flateWriter.Write([]byte(responseBody))
		flateWriter.Close()
		compressedBody := buf.Bytes()

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "deflate")
			w.WriteHeader(http.StatusOK)
			w.Write(compressedBody)
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 200, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 75, metrics[0].Tokens.OutputTokens)
	})

	t.Run("invalid gzip data records minimal metrics", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		// Invalid compressed data
		invalidData := []byte("this is not gzip data")

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusOK)
			w.Write(invalidData)
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err) // Should not return error, just log warning

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, "test-model", metrics[0].Model)
		assert.Equal(t, 0, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 0, metrics[0].Tokens.OutputTokens)
	})

	t.Run("unknown encoding treated as uncompressed", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		responseBody := `{"usage": {"prompt_tokens": 300, "completion_tokens": 100}}`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "unknown-encoding")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		assert.Equal(t, 300, metrics[0].Tokens.InputTokens)
		assert.Equal(t, 100, metrics[0].Tokens.OutputTokens)
	})
}

func TestReqRespCapture_CompressedSize(t *testing.T) {
	t.Run("compressed size is smaller than uncompressed", func(t *testing.T) {
		capture := ReqRespCapture{
			ID:       1,
			ReqPath:  "/v1/chat/completions",
			ReqBody:  []byte(`{"model":"test","prompt":"hello world this is a test request body that is reasonably long"}`),
			RespBody: []byte(`{"id":"resp-123","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"This is a test response body with some meaningful content to compress"}},{"index":1,"message":{"role":"user","content":"Another message here"}}]}`),
		}

		compressed, uncompressed, err := compressCapture(&capture)
		assert.NoError(t, err)
		assert.Greater(t, uncompressed, 0)
		assert.True(t, len(compressed) < uncompressed, "compressed (%d bytes) should be smaller than uncompressed JSON (%d bytes)", len(compressed), uncompressed)
	})

	t.Run("empty capture produces compressed output", func(t *testing.T) {
		capture := ReqRespCapture{}
		compressed, _, err := compressCapture(&capture)
		assert.NoError(t, err)
		assert.NotNil(t, compressed)
		assert.True(t, len(compressed) > 0)
	})
}

func TestMetricsMonitor_AddCapture(t *testing.T) {
	t.Run("does nothing when captures disabled", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		capture := ReqRespCapture{
			ID:      0,
			ReqBody: []byte("test"),
		}
		mm.addCapture(capture)

		// Should not store capture
		assert.Nil(t, mm.getCaptureByID(0))
	})

	t.Run("adds capture when enabled", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 5)

		capture := ReqRespCapture{
			ID:       0,
			ReqBody:  []byte("test request"),
			RespBody: []byte("test response"),
		}
		mm.addCapture(capture)

		captured := mm.getCaptureByID(0)
		assert.NotNil(t, captured)
		assert.Equal(t, 0, captured.ID)
		assert.Equal(t, []byte("test request"), captured.ReqBody)
		assert.Equal(t, []byte("test response"), captured.RespBody)
	})

	t.Run("evicts oldest when exceeding max size", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 5)
		// Each full ReqRespCapture with 80 bytes random data compresses to ~185 bytes.
		// 2 captures = ~370 bytes, 3 captures = ~555 bytes. Set limit so only 2 fit.
		mm.captureCache = cache.New(450)

		// Use random-looking data that doesn't compress well with zstd
		rng := rand.New(rand.NewSource(42))
		capture1 := ReqRespCapture{ID: 0, ReqBody: make([]byte, 80)}
		rng.Read(capture1.ReqBody)
		capture2 := ReqRespCapture{ID: 1, ReqBody: make([]byte, 80)}
		rng.Read(capture2.ReqBody)
		capture3 := ReqRespCapture{ID: 2, ReqBody: make([]byte, 80)}
		rng.Read(capture3.ReqBody)

		mm.addCapture(capture1)
		mm.addCapture(capture2)
		// Adding capture3 should evict capture1
		mm.addCapture(capture3)

		assert.Nil(t, mm.getCaptureByID(0), "capture 0 should be evicted")
		assert.NotNil(t, mm.getCaptureByID(1), "capture 1 should exist")
		assert.NotNil(t, mm.getCaptureByID(2), "capture 2 should exist")
	})

	t.Run("skips capture larger than max size", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 5)
		mm.captureCache = cache.New(100)

		// Use random data that doesn't compress well to create an oversized capture
		rng := rand.New(rand.NewSource(99))
		largeCapture := ReqRespCapture{ID: 0, ReqBody: make([]byte, 300)}
		rng.Read(largeCapture.ReqBody)
		mm.addCapture(largeCapture)

		assert.Nil(t, mm.getCaptureByID(0), "oversized capture should not be stored")
	})
}

func TestMetricsMonitor_GetCaptureByID(t *testing.T) {
	t.Run("returns nil for non-existent ID", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 5)

		assert.Nil(t, mm.getCaptureByID(999))
	})

	t.Run("returns decompressed capture by ID", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 5)

		capture := ReqRespCapture{
			ID:       42,
			ReqBody:  []byte("test request"),
			RespBody: []byte("test response"),
		}
		mm.addCapture(capture)

		captured := mm.getCaptureByID(42)
		assert.NotNil(t, captured)
		assert.Equal(t, 42, captured.ID)
		assert.Equal(t, []byte("test request"), captured.ReqBody)
		assert.Equal(t, []byte("test response"), captured.RespBody)
	})

	t.Run("stores data as compressed bytes", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 5)

		capture := ReqRespCapture{
			ID:       42,
			ReqBody:  []byte("test request body"),
			RespBody: []byte("test response body"),
		}
		mm.addCapture(capture)

		compressed, exists := mm.getCompressedBytes(42)
		assert.True(t, exists)
		assert.NotNil(t, compressed)
		// Compressed data should not be valid CBOR (it's zstd-compressed)
		var decoded ReqRespCapture
		assert.Error(t, cbor.Unmarshal(compressed, &decoded))
	})
}

func TestRedactHeaders(t *testing.T) {
	t.Run("redacts sensitive headers", func(t *testing.T) {
		headers := map[string]string{
			"Authorization":       "Bearer secret-token",
			"Proxy-Authorization": "Basic creds",
			"Cookie":              "session=abc123",
			"Set-Cookie":          "session=xyz789",
			"X-Api-Key":           "sk-12345",
			"Content-Type":        "application/json",
			"X-Custom":            "safe-value",
		}

		redactHeaders(headers)

		assert.Equal(t, "[REDACTED]", headers["Authorization"])
		assert.Equal(t, "[REDACTED]", headers["Proxy-Authorization"])
		assert.Equal(t, "[REDACTED]", headers["Cookie"])
		assert.Equal(t, "[REDACTED]", headers["Set-Cookie"])
		assert.Equal(t, "[REDACTED]", headers["X-Api-Key"])
		assert.Equal(t, "application/json", headers["Content-Type"])
		assert.Equal(t, "safe-value", headers["X-Custom"])
	})

	t.Run("handles mixed case header names", func(t *testing.T) {
		headers := map[string]string{
			"authorization": "Bearer token",
			"COOKIE":        "session=abc",
			"x-api-key":     "key123",
		}

		redactHeaders(headers)

		assert.Equal(t, "[REDACTED]", headers["authorization"])
		assert.Equal(t, "[REDACTED]", headers["COOKIE"])
		assert.Equal(t, "[REDACTED]", headers["x-api-key"])
	})

	t.Run("handles empty headers", func(t *testing.T) {
		headers := map[string]string{}
		redactHeaders(headers)
		assert.Empty(t, headers)
	})
}

func TestMetricsMonitor_WrapHandler_Capture(t *testing.T) {
	t.Run("captures request and response when enabled", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 5)

		requestBody := `{"model": "test", "prompt": "hello"}`
		responseBody := `{"usage": {"prompt_tokens": 100, "completion_tokens": 50}}`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Custom", "header-value")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		// Check metric was recorded
		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))
		metricID := metrics[0].ID

		// Check capture was stored with same ID (decompressed)
		capture := mm.getCaptureByID(metricID)
		assert.NotNil(t, capture)
		assert.Equal(t, metricID, capture.ID)
		assert.Equal(t, []byte(requestBody), capture.ReqBody)
		assert.Equal(t, []byte(responseBody), capture.RespBody)
		assert.Equal(t, "/test", capture.ReqPath)
		assert.Equal(t, "application/json", capture.ReqHeaders["Content-Type"])
		assert.Equal(t, "[REDACTED]", capture.ReqHeaders["Authorization"])
		assert.Equal(t, "application/json", capture.RespHeaders["Content-Type"])
		assert.Equal(t, "header-value", capture.RespHeaders["X-Custom"])
	})

	t.Run("does not capture when disabled", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 0)

		requestBody := `{"model": "test"}`
		responseBody := `{"usage": {"prompt_tokens": 100, "completion_tokens": 50}}`

		nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(responseBody))
			return nil
		}

		req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBody))
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureAll, nextHandler)
		assert.NoError(t, err)

		// Metrics should still be recorded
		metrics := mm.getMetrics()
		assert.Equal(t, 1, len(metrics))

		// But no capture
		assert.Nil(t, mm.getCaptureByID(metrics[0].ID))
	})
}

func TestMetricsMonitor_WrapHandler_PartialCaptures(t *testing.T) {
	requestBody := `{"model": "test"}`
	responseBody := `{"usage": {"prompt_tokens": 100, "completion_tokens": 50}}`

	nextHandler := func(modelID string, w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "header-value")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(responseBody))
		return nil
	}

	t.Run("only request headers", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 100)
		req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureReqHeaders, nextHandler)
		assert.NoError(t, err)

		capture := mm.getCaptureByID(mm.getMetrics()[0].ID)
		assert.NotNil(t, capture)
		assert.Equal(t, "application/json", capture.ReqHeaders["Content-Type"])
		assert.Equal(t, "[REDACTED]", capture.ReqHeaders["Authorization"])
		assert.Nil(t, capture.ReqBody)
		assert.Nil(t, capture.RespHeaders)
		assert.Nil(t, capture.RespBody)
	})

	t.Run("only request body", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 100)
		req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureReqBody, nextHandler)
		assert.NoError(t, err)

		capture := mm.getCaptureByID(mm.getMetrics()[0].ID)
		assert.NotNil(t, capture)
		assert.Nil(t, capture.ReqHeaders)
		assert.Equal(t, []byte(requestBody), capture.ReqBody)
		assert.Nil(t, capture.RespHeaders)
		assert.Nil(t, capture.RespBody)
	})

	t.Run("only response headers", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 100)
		req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBody))
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureRespHeaders, nextHandler)
		assert.NoError(t, err)

		capture := mm.getCaptureByID(mm.getMetrics()[0].ID)
		assert.NotNil(t, capture)
		assert.Nil(t, capture.ReqHeaders)
		assert.Nil(t, capture.ReqBody)
		assert.Equal(t, "application/json", capture.RespHeaders["Content-Type"])
		assert.Equal(t, "header-value", capture.RespHeaders["X-Custom"])
		assert.Nil(t, capture.RespBody)
	})

	t.Run("only response body", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 100)
		req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBody))
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureRespBody, nextHandler)
		assert.NoError(t, err)

		capture := mm.getCaptureByID(mm.getMetrics()[0].ID)
		assert.NotNil(t, capture)
		assert.Nil(t, capture.ReqHeaders)
		assert.Nil(t, capture.ReqBody)
		assert.Nil(t, capture.RespHeaders)
		assert.Equal(t, []byte(responseBody), capture.RespBody)
	})

	t.Run("captureReqAll", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 100)
		req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureReqAll, nextHandler)
		assert.NoError(t, err)

		capture := mm.getCaptureByID(mm.getMetrics()[0].ID)
		assert.NotNil(t, capture)
		assert.Equal(t, "application/json", capture.ReqHeaders["Content-Type"])
		assert.Equal(t, "[REDACTED]", capture.ReqHeaders["Authorization"])
		assert.Equal(t, []byte(requestBody), capture.ReqBody)
		assert.Nil(t, capture.RespHeaders)
		assert.Nil(t, capture.RespBody)
	})

	t.Run("captureRespAll", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 100)
		req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBody))
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureRespAll, nextHandler)
		assert.NoError(t, err)

		capture := mm.getCaptureByID(mm.getMetrics()[0].ID)
		assert.NotNil(t, capture)
		assert.Nil(t, capture.ReqHeaders)
		assert.Nil(t, capture.ReqBody)
		assert.Equal(t, "application/json", capture.RespHeaders["Content-Type"])
		assert.Equal(t, "header-value", capture.RespHeaders["X-Custom"])
		assert.Equal(t, []byte(responseBody), capture.RespBody)
	})

	t.Run("no flags", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 100)
		req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureFields(0), nextHandler)
		assert.NoError(t, err)

		capture := mm.getCaptureByID(mm.getMetrics()[0].ID)
		assert.NotNil(t, capture)
		assert.Nil(t, capture.ReqHeaders)
		assert.Nil(t, capture.ReqBody)
		assert.Nil(t, capture.RespHeaders)
		assert.Nil(t, capture.RespBody)
	})

	t.Run("mixed flags req headers and resp body", func(t *testing.T) {
		mm := newMetricsMonitor(testLogger, 10, 100)
		req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)

		err := mm.wrapHandler("test-model", ginCtx.Writer, req, captureReqHeaders|captureRespBody, nextHandler)
		assert.NoError(t, err)

		capture := mm.getCaptureByID(mm.getMetrics()[0].ID)
		assert.NotNil(t, capture)
		assert.Equal(t, "application/json", capture.ReqHeaders["Content-Type"])
		assert.Equal(t, "[REDACTED]", capture.ReqHeaders["Authorization"])
		assert.Nil(t, capture.ReqBody)
		assert.Nil(t, capture.RespHeaders)
		assert.Equal(t, []byte(responseBody), capture.RespBody)
	})
}
