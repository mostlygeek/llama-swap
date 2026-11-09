// Package genaimetrics turns finished requests into OpenTelemetry GenAI
// semantic convention histograms and exports them over OTLP with the
// OpenTelemetry Go SDK.
//
// It is opt-in through the standard OTEL_* environment variables: nothing is
// created unless an OTLP endpoint is set. The SDK reads the rest (headers,
// timeout, compression, TLS, export interval, resource attributes).
package genaimetrics

import (
	"context"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/store"
)

// Bucket boundaries advised by the GenAI semantic conventions. The SDK's
// defaults are tuned for milliseconds and would put most requests in one
// bucket.
var (
	durationBounds = []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}
	tokenBounds    = []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864}
	tpotBounds     = []float64{0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.2, 0.3, 0.4, 0.5, 0.75, 1.0, 2.5}
)

// Metrics records GenAI histograms for finished requests.
type Metrics struct {
	provider *sdkmetric.MeterProvider
	duration genaiconv.ServerRequestDuration
	tokens   genaiconv.ClientTokenUsage
	tpot     genaiconv.ServerTimePerOutputToken
}

// FromEnv builds Metrics exporting over OTLP/HTTP. It returns nil, nil when
// no OTLP endpoint is set or metrics are disabled, so callers pay nothing
// unless the user opted in.
func FromEnv(ctx context.Context, getenv func(string) string, version string, logger *logmon.Monitor) (*Metrics, error) {
	if strings.EqualFold(getenv("OTEL_SDK_DISABLED"), "true") || getenv("OTEL_METRICS_EXPORTER") == "none" {
		return nil, nil
	}
	// The exporter would fall back to localhost:4318; require an explicit
	// endpoint so the feature stays off by default.
	if getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") == "" {
		return nil, nil
	}
	protocol := getenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL")
	if protocol == "" {
		protocol = getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
	}
	if protocol != "" && protocol != "http/protobuf" && logger != nil {
		logger.Warnf("OTLP protocol %q is not supported, sending metrics as http/protobuf", protocol)
	}

	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}
	// Later options win, so OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES
	// override the defaults.
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName("llama-swap"), semconv.ServiceVersion(version)),
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, err
	}
	m, err := newMetrics(sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
	))
	if err == nil && logger != nil {
		logger.Info("OTLP GenAI metrics enabled")
	}
	return m, err
}

func newMetrics(provider *sdkmetric.MeterProvider) (*Metrics, error) {
	meter := provider.Meter("github.com/mostlygeek/llama-swap")
	m := &Metrics{provider: provider}
	var err error
	if m.duration, err = genaiconv.NewServerRequestDuration(meter, metric.WithExplicitBucketBoundaries(durationBounds...)); err != nil {
		return nil, err
	}
	if m.tokens, err = genaiconv.NewClientTokenUsage(meter, metric.WithExplicitBucketBoundaries(tokenBounds...)); err != nil {
		return nil, err
	}
	if m.tpot, err = genaiconv.NewServerTimePerOutputToken(meter, metric.WithExplicitBucketBoundaries(tpotBounds...)); err != nil {
		return nil, err
	}
	return m, nil
}

// Shutdown flushes what was recorded since the last export and stops the
// exporter.
func (m *Metrics) Shutdown(ctx context.Context) error {
	return m.provider.Shutdown(ctx)
}

// Record adds one finished request. upstream is the kind of server that
// answered it, as detected by the router.
func (m *Metrics) Record(entry store.ActivityLogEntry, upstream hint.Upstream) {
	ctx := context.Background()
	op := operationName(entry)
	provider := providerName(upstream)
	model := m.duration.AttrRequestModel(entry.Model)
	failed := entry.RespStatusCode >= 400

	// Includes loading the model when this request swapped it in.
	attrs := []attribute.KeyValue{model}
	if failed {
		attrs = append(attrs, m.duration.AttrErrorType(genaiconv.ErrorTypeAttr(strconv.Itoa(entry.RespStatusCode))))
	}
	m.duration.Record(ctx, float64(entry.DurationMs)/1000, op, provider, attrs...)

	// Zero means unknown here: failed requests carry zeroed token counts.
	if n := entry.Tokens.InputTokens; n > 0 {
		m.tokens.Record(ctx, int64(n), op, provider, genaiconv.TokenTypeInput, model)
	}
	if n := entry.Tokens.OutputTokens; n > 0 {
		m.tokens.Record(ctx, int64(n), op, provider, genaiconv.TokenTypeOutput, model)
	}

	// TokensPerSecond is the decode rate (prefill excluded), so its inverse
	// is the time per output token after the first one.
	if !failed && entry.Tokens.TokensPerSecond > 0 {
		m.tpot.Record(ctx, 1/entry.Tokens.TokensPerSecond, op, provider, model)
	}
}

// operationName maps a request to a gen_ai.operation.name. Tasks without a
// well-known value keep llama-swap's task name (rerank, speech, ...).
func operationName(e store.ActivityLogEntry) genaiconv.OperationNameAttr {
	switch hint.Task(e.Task) {
	case hint.TaskChat:
		p := e.ReqPath
		if (strings.HasSuffix(p, "/completions") && !strings.HasSuffix(p, "/chat/completions")) ||
			strings.HasSuffix(p, "/completion") || strings.HasSuffix(p, "/infill") {
			return genaiconv.OperationNameTextCompletion
		}
		return genaiconv.OperationNameChat
	case hint.TaskEmbedding:
		return genaiconv.OperationNameEmbeddings
	case hint.TaskRealtime:
		return "realtime_inference"
	case hint.TaskUnknown:
		return "unknown"
	}
	return genaiconv.OperationNameAttr(e.Task)
}

// providerName maps the detected upstream to a gen_ai.provider.name. The
// semantic conventions' well-known values (openai, anthropic) match the hint
// names; llama-server is reported under its project name.
func providerName(u hint.Upstream) genaiconv.ProviderNameAttr {
	switch u {
	case hint.LlamaServer:
		return "llama.cpp"
	case hint.Unknown:
		return "unknown"
	}
	return genaiconv.ProviderNameAttr(u)
}
