package genaimetrics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestGenAIMetrics_FromEnvOptIn(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no endpoint":  {"OTEL_SERVICE_NAME": "x"},
		"sdk disabled": {"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318", "OTEL_SDK_DISABLED": "true"},
		"exporter off": {"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318", "OTEL_METRICS_EXPORTER": "none"},
	} {
		e, err := FromEnv(envOf(env), "v1", nil)
		require.NoError(t, err, name)
		assert.Nil(t, e, name)
	}

	_, err := FromEnv(envOf(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318", "OTEL_METRIC_EXPORT_INTERVAL": "10s"}), "v1", nil)
	assert.ErrorContains(t, err, "OTEL_METRIC_EXPORT_INTERVAL")
}

func TestGenAIMetrics_FromEnvSettings(t *testing.T) {
	e, err := FromEnv(envOf(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318/",
		"OTEL_EXPORTER_OTLP_HEADERS":  "Authorization=Bearer%20abc, Dash0-Dataset=homelab",
		"OTEL_RESOURCE_ATTRIBUTES":    "service.name=ignored,k8s.namespace.name=llm",
		"OTEL_SERVICE_NAME":           "swap",
		"OTEL_METRIC_EXPORT_INTERVAL": "5000",
	}), "v271", nil)
	require.NoError(t, err)
	assert.Equal(t, "http://collector:4318/v1/metrics", e.url)
	assert.Equal(t, map[string]string{"Authorization": "Bearer abc", "Dash0-Dataset": "homelab"}, e.headers)
	assert.Equal(t, map[string]string{"service.name": "swap", "service.version": "v271", "k8s.namespace.name": "llm"}, e.resource)
	assert.Equal(t, 5*time.Second, e.interval)

	// The signal-specific endpoint is used as-is.
	e, err = FromEnv(envOf(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://ignored:4318",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "https://ingress.example/otlp/metrics",
	}), "", nil)
	require.NoError(t, err)
	assert.Equal(t, "https://ingress.example/otlp/metrics", e.url)
	assert.Equal(t, map[string]string{"service.name": "llama-swap"}, e.resource)
}

// TestGenAIMetrics_Push records a few requests and checks what an OTLP
// receiver gets: semconv names, attributes and bucket placement.
func TestGenAIMetrics_Push(t *testing.T) {
	var got otlpRequest
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Authorization")
		assert.Equal(t, "/v1/metrics", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &got))
	}))
	defer srv.Close()

	e, err := FromEnv(envOf(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": srv.URL,
		"OTEL_EXPORTER_OTLP_HEADERS":  "Authorization=token",
	}), "", nil)
	require.NoError(t, err)

	chat := store.ActivityLogEntry{
		Model: "qwen", Task: "chat", ReqPath: "/v1/chat/completions", RespStatusCode: 200, DurationMs: 1500,
		Tokens: store.TokenMetrics{InputTokens: 100, OutputTokens: 50, TokensPerSecond: 20},
	}
	e.Record(chat, hint.LlamaServer)
	e.Record(chat, hint.LlamaServer)
	// A failed request: zeroed tokens are not usage, and no decode rate.
	e.Record(store.ActivityLogEntry{Model: "qwen", Task: "chat", ReqPath: "/v1/completions", RespStatusCode: 503, DurationMs: 10}, hint.Unknown)

	require.NoError(t, e.push(context.Background()))
	assert.Equal(t, "token", gotHeader)

	metrics := got.ResourceMetrics[0].ScopeMetrics[0].Metrics
	require.Len(t, metrics, 3)
	byName := map[string]otlpMetric{}
	for _, m := range metrics {
		assert.Equal(t, cumulative, m.Histogram.AggregationTemporality)
		byName[m.Name] = m
	}

	attrs := func(p otlpPoint) map[string]string {
		out := map[string]string{}
		for _, a := range p.Attributes {
			out[a.Key] = a.Value.StringValue
		}
		return out
	}
	find := func(name string, want map[string]string) otlpPoint {
		t.Helper()
		for _, p := range byName[name].Histogram.DataPoints {
			if assert.ObjectsAreEqual(want, attrs(p)) {
				return p
			}
		}
		t.Fatalf("%s: no point with %v", name, want)
		return otlpPoint{}
	}
	ok := map[string]string{"gen_ai.operation.name": "chat", "gen_ai.provider.name": "llama.cpp", "gen_ai.request.model": "qwen"}
	with := func(k, v string) map[string]string {
		m := map[string]string{k: v}
		for k, v := range ok {
			m[k] = v
		}
		return m
	}

	// 1.5 s falls in (1.28, 2.56], bucket 8.
	d := find("gen_ai.server.request.duration", ok)
	assert.Equal(t, uint64(2), d.Count)
	assert.Equal(t, 3.0, d.Sum)
	assert.Equal(t, uint64(2), d.BucketCounts[8])
	assert.Len(t, d.BucketCounts, len(d.ExplicitBounds)+1)

	failed := find("gen_ai.server.request.duration", map[string]string{
		"gen_ai.operation.name": "text_completion", "gen_ai.provider.name": "unknown",
		"gen_ai.request.model": "qwen", "error.type": "503",
	})
	assert.Equal(t, uint64(1), failed.Count)

	in := find("gen_ai.client.token.usage", with("gen_ai.token.type", "input"))
	assert.Equal(t, 200.0, in.Sum)
	out := find("gen_ai.client.token.usage", with("gen_ai.token.type", "output"))
	assert.Equal(t, 100.0, out.Sum)
	assert.Len(t, byName["gen_ai.client.token.usage"].Histogram.DataPoints, 2)

	// 1/20 tok/s = 0.05 s sits exactly on a bound, so (0.025, 0.05], bucket 2.
	tpot := find("gen_ai.server.time_per_output_token", ok)
	assert.Equal(t, uint64(2), tpot.BucketCounts[2])
	assert.Len(t, byName["gen_ai.server.time_per_output_token"].Histogram.DataPoints, 1)
}

func TestGenAIMetrics_PushError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad payload", http.StatusBadRequest)
	}))
	defer srv.Close()
	e, err := FromEnv(envOf(map[string]string{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": srv.URL}), "", nil)
	require.NoError(t, err)

	// Nothing recorded: no request is sent.
	require.NoError(t, e.push(context.Background()))

	e.Record(store.ActivityLogEntry{Model: "m", Task: "embedding", RespStatusCode: 200, DurationMs: 5}, hint.VLLM)
	assert.ErrorContains(t, e.push(context.Background()), "bad payload")
}
