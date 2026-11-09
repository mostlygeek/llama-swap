package genaimetrics

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/mostlygeek/llama-swap/internal/store"
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
		m, err := FromEnv(context.Background(), envOf(env), "v1", nil)
		require.NoError(t, err, name)
		assert.Nil(t, m, name)
	}

	// The SDK reads its settings from the real environment.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	m, err := FromEnv(context.Background(), envOf(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:1"}), "v1", nil)
	require.NoError(t, err)
	require.NotNil(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // don't wait on the unreachable endpoint
	_ = m.Shutdown(ctx)
}

// TestGenAIMetrics_Record checks names, attributes and bucket placement of
// what a few requests produce.
func TestGenAIMetrics_Record(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	m, err := newMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	require.NoError(t, err)

	chat := store.ActivityLogEntry{
		Model: "qwen", Task: "chat", ReqPath: "/v1/chat/completions", RespStatusCode: 200, DurationMs: 1500,
		Tokens: store.TokenMetrics{InputTokens: 100, OutputTokens: 50, TokensPerSecond: 20},
	}
	m.Record(chat, hint.LlamaServer)
	m.Record(chat, hint.LlamaServer)
	// A failed request: zeroed tokens are not usage, and no decode rate.
	m.Record(store.ActivityLogEntry{Model: "qwen", Task: "chat", ReqPath: "/v1/completions", RespStatusCode: 503, DurationMs: 10}, hint.Unknown)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.Len(t, rm.ScopeMetrics, 1)

	byName := map[string]metricdata.Metrics{}
	for _, metric := range rm.ScopeMetrics[0].Metrics {
		byName[metric.Name] = metric
	}
	require.Len(t, byName, 3)
	assert.Equal(t, "s", byName["gen_ai.server.request.duration"].Unit)
	assert.Equal(t, "{token}", byName["gen_ai.client.token.usage"].Unit)

	ok := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.provider.name", "llama.cpp"),
		attribute.String("gen_ai.request.model", "qwen"),
	}
	with := func(kv ...attribute.KeyValue) attribute.Set { return attribute.NewSet(append(kv, ok...)...) }

	// 1.5 s falls in (1.28, 2.56], bucket 8.
	duration := points[float64](t, byName["gen_ai.server.request.duration"])
	require.Len(t, duration, 2)
	d := duration[with()]
	assert.Equal(t, uint64(2), d.Count)
	assert.Equal(t, 3.0, d.Sum)
	assert.Equal(t, uint64(2), d.BucketCounts[8])
	assert.Equal(t, durationBounds, d.Bounds)

	failed := duration[attribute.NewSet(
		attribute.String("gen_ai.operation.name", "text_completion"),
		attribute.String("gen_ai.provider.name", "unknown"),
		attribute.String("gen_ai.request.model", "qwen"),
		attribute.String("error.type", "503"),
	)]
	assert.Equal(t, uint64(1), failed.Count)

	tokens := points[int64](t, byName["gen_ai.client.token.usage"])
	require.Len(t, tokens, 2)
	assert.Equal(t, int64(200), tokens[with(attribute.String("gen_ai.token.type", "input"))].Sum)
	assert.Equal(t, int64(100), tokens[with(attribute.String("gen_ai.token.type", "output"))].Sum)

	// 1/20 tok/s = 0.05 s sits exactly on a bound, so (0.025, 0.05], bucket 2.
	tpot := points[float64](t, byName["gen_ai.server.time_per_output_token"])
	require.Len(t, tpot, 1)
	assert.Equal(t, uint64(2), tpot[with()].BucketCounts[2])
}

func points[N int64 | float64](t *testing.T, m metricdata.Metrics) map[attribute.Set]metricdata.HistogramDataPoint[N] {
	t.Helper()
	h, ok := m.Data.(metricdata.Histogram[N])
	require.True(t, ok, "%s is %T", m.Name, m.Data)
	assert.Equal(t, metricdata.CumulativeTemporality, h.Temporality)
	out := map[attribute.Set]metricdata.HistogramDataPoint[N]{}
	for _, p := range h.DataPoints {
		out[p.Attributes] = p
	}
	return out
}

func TestGenAIMetrics_ExporterProtocol(t *testing.T) {
	for protocol, want := range map[string]string{
		"":              "*otlpmetrichttp.Exporter",
		"http/protobuf": "*otlpmetrichttp.Exporter",
		"http/json":     "*otlpmetrichttp.Exporter", // unsupported: falls back with a warning
		"grpc":          "*otlpmetricgrpc.Exporter",
	} {
		exp, err := newExporter(context.Background(), envOf(map[string]string{"OTEL_EXPORTER_OTLP_PROTOCOL": protocol}), nil)
		require.NoError(t, err, protocol)
		assert.Equal(t, want, fmt.Sprintf("%T", exp), protocol)
	}

	// The signal-specific variable wins.
	exp, err := newExporter(context.Background(), envOf(map[string]string{
		"OTEL_EXPORTER_OTLP_PROTOCOL":         "http/protobuf",
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "grpc",
	}), nil)
	require.NoError(t, err)
	assert.Equal(t, "*otlpmetricgrpc.Exporter", fmt.Sprintf("%T", exp))
}
