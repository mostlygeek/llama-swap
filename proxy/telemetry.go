package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/tidwall/gjson"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const defaultLangfuseOTLPEndpoint = "http://localhost:3000/api/public/otel/v1/traces"

type TelemetryManager struct {
	tp              *sdktrace.TracerProvider
	tracer          trace.Tracer
	serviceName     string
	environment     string
	captureInput    bool
	captureOutput   bool
	maxContentBytes int
	enabled         bool
}

func NewTelemetryManager(cfg config.TelemetryConfig) (*TelemetryManager, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	publicKey := strings.TrimSpace(cfg.OTLP.PublicKey)
	secretKey := strings.TrimSpace(cfg.OTLP.SecretKey)
	if publicKey == "" || secretKey == "" {
		return nil, fmt.Errorf("telemetry.otlp.publicKey and telemetry.otlp.secretKey are required when telemetry is enabled")
	}

	endpoint := strings.TrimSpace(cfg.OTLP.Endpoint)
	if endpoint == "" {
		endpoint = defaultLangfuseOTLPEndpoint
	}

	headers := make(map[string]string, len(cfg.OTLP.Headers)+2)
	for key, value := range cfg.OTLP.Headers {
		headers[key] = value
	}
	headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(publicKey+":"+secretKey))
	if _, ok := headers["x-langfuse-ingestion-version"]; !ok {
		headers["x-langfuse-ingestion-version"] = "4"
	}

	exporterOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithHeaders(headers),
	}
	if cfg.OTLP.Insecure || strings.HasPrefix(strings.ToLower(endpoint), "http://") {
		exporterOpts = append(exporterOpts, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(context.Background(), exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("create otlp exporter: %w", err)
	}

	serviceName := strings.TrimSpace(cfg.ServiceName)
	if serviceName == "" {
		serviceName = "llama-swap"
	}
	environment := strings.TrimSpace(cfg.Environment)

	sampleRatio := cfg.SampleRatio
	if sampleRatio <= 0 {
		sampleRatio = 1
	}

	sampler := sdktrace.AlwaysSample()
	switch {
	case sampleRatio < 1:
		sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRatio))
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create otel resource: %w", err)
	}
	if environment != "" {
		res, err = resource.Merge(res, resource.NewWithAttributes("", attribute.String("deployment.environment", environment)))
		if err != nil {
			return nil, fmt.Errorf("create otel resource: %w", err)
		}
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
	)

	return &TelemetryManager{
		tp:              tp,
		tracer:          tp.Tracer("github.com/mostlygeek/llama-swap/proxy"),
		serviceName:     serviceName,
		environment:     environment,
		captureInput:    cfg.CaptureInput,
		captureOutput:   cfg.CaptureOutput,
		maxContentBytes: maxContentBytesOrDefault(cfg.MaxContentBytes, 32768),
		enabled:         true,
	}, nil
}

func (tm *TelemetryManager) Shutdown(ctx context.Context) error {
	if tm == nil || tm.tp == nil {
		return nil
	}
	return tm.tp.Shutdown(ctx)
}

func (tm *TelemetryManager) RecordActivity(
	ctx context.Context,
	metric ActivityLogEntry,
	request *http.Request,
	reqBody []byte,
	respBody []byte,
) {
	if tm == nil || !tm.enabled || tm.tp == nil {
		return
	}

	route := metric.ReqPath
	if route == "" && request != nil {
		route = request.URL.Path
	}
	responseModel := responseModelFromBody(metric.RespContentType, respBody)

	spanName := "llama-swap " + route
	startTime := metric.Timestamp
	if startTime.IsZero() {
		startTime = time.Now()
	}
	if metric.DurationMs > 0 {
		startTime = startTime.Add(-time.Duration(metric.DurationMs) * time.Millisecond)
	}

	_, span := tm.tracer.Start(
		ctx,
		spanName,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithTimestamp(startTime),
	)
	defer span.End(trace.WithTimestamp(metricTimestampOrNow(metric.Timestamp)))

	streaming := false
	if request != nil {
		if val, ok := request.Context().Value(proxyCtxKey("streaming")).(bool); ok {
			streaming = val
		}
	}

	span.SetAttributes(
		attribute.String("langfuse.trace.name", spanName),
		attribute.String("langfuse.observation.type", "generation"),
		attribute.String("langfuse.observation.metadata.route", route),
		attribute.String("langfuse.observation.metadata.model", metric.Model),
		attribute.String("langfuse.observation.metadata.resp_content_type", metric.RespContentType),
		attribute.String("langfuse.observation.metadata.streaming", fmt.Sprintf("%t", streaming)),
		attribute.Int("langfuse.observation.metadata.activity_id", metric.ID),
		attribute.String("gen_ai.operation.name", operationFromPath(route)),
		attribute.String("gen_ai.request.model", metric.Model),
		attribute.String("gen_ai.response.model", responseModel),
		attribute.String("langfuse.observation.model.name", responseModel),
		attribute.Int("http.response.status_code", metric.RespStatusCode),
		attribute.Int("llama_swap.duration_ms", metric.DurationMs),
		attribute.String("llama_swap.service_name", tm.serviceName),
		attribute.Bool("llama_swap.has_capture", metric.HasCapture),
	)

	if tm.environment != "" {
		span.SetAttributes(attribute.String("langfuse.environment", tm.environment))
	}

	if metric.Tokens.InputTokens >= 0 {
		span.SetAttributes(attribute.Int("gen_ai.usage.input_tokens", metric.Tokens.InputTokens))
	}
	if metric.Tokens.OutputTokens >= 0 {
		span.SetAttributes(attribute.Int("gen_ai.usage.output_tokens", metric.Tokens.OutputTokens))
	}
	if metric.Tokens.CachedTokens >= 0 {
		span.SetAttributes(attribute.Int("llama_swap.usage.cached_tokens", metric.Tokens.CachedTokens))
	}
	if metric.Tokens.PromptPerSecond >= 0 {
		span.SetAttributes(attribute.Float64("llama_swap.performance.prompt_tokens_per_second", metric.Tokens.PromptPerSecond))
	}
	if metric.Tokens.TokensPerSecond >= 0 {
		span.SetAttributes(attribute.Float64("llama_swap.performance.tokens_per_second", metric.Tokens.TokensPerSecond))
	}

	if metric.RespStatusCode >= 400 {
		span.SetStatus(codes.Error, http.StatusText(metric.RespStatusCode))
	}

	if tm.captureInput && len(reqBody) > 0 && request != nil && isTextualContentType(request.Header.Get("Content-Type")) {
		if text := truncateText(reqBody, tm.maxContentBytes); text != "" {
			span.SetAttributes(attribute.String("gen_ai.prompt", text))
		}
	}

	if tm.captureOutput && len(respBody) > 0 && isTextualContentType(metric.RespContentType) {
		if text, _ := responseTextFromBody(metric.RespContentType, respBody); text != "" {
			span.SetAttributes(attribute.String("gen_ai.completion", truncateText([]byte(text), tm.maxContentBytes)))
		}
	}
}

func metricTimestampOrNow(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Now()
	}
	return ts
}

func responseModelFromBody(contentType string, body []byte) string {
	if len(body) == 0 {
		return ""
	}

	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "event-stream") {
		return responseModelFromStreamingBody(body)
	}

	if !gjson.ValidBytes(body) {
		return ""
	}

	parsed := gjson.ParseBytes(body)
	if model := parsed.Get("model"); model.Exists() {
		return model.String()
	}
	if model := parsed.Get("response.model"); model.Exists() {
		return model.String()
	}
	return ""
}

func responseModelFromStreamingBody(body []byte) string {
	pos := len(body)
	for pos > 0 {
		lineStart := bytes.LastIndexByte(body[:pos], '\n')
		if lineStart == -1 {
			lineStart = 0
		} else {
			lineStart++
		}

		line := bytes.TrimSpace(body[lineStart:pos])
		pos = lineStart - 1

		if len(line) == 0 {
			continue
		}

		prefix := []byte("data:")
		if !bytes.HasPrefix(line, prefix) {
			continue
		}
		data := bytes.TrimSpace(line[len(prefix):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		if !gjson.ValidBytes(data) {
			continue
		}

		parsed := gjson.ParseBytes(data)
		if model := parsed.Get("model"); model.Exists() {
			return model.String()
		}
		if model := parsed.Get("response.model"); model.Exists() {
			return model.String()
		}
	}

	return ""
}

func responseTextFromBody(contentType string, body []byte) (string, string) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "event-stream") {
		return responseTextFromStreamingBody(body)
	}

	if !gjson.ValidBytes(body) {
		return "", ""
	}

	parsed := gjson.ParseBytes(body)
	text, reasoning := extractTextFromJSON(parsed)
	return text, reasoning
}

func responseTextFromStreamingBody(body []byte) (string, string) {
	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder

	pos := len(body)
	for pos > 0 {
		lineStart := bytes.LastIndexByte(body[:pos], '\n')
		if lineStart == -1 {
			lineStart = 0
		} else {
			lineStart++
		}

		line := bytes.TrimSpace(body[lineStart:pos])
		pos = lineStart - 1

		if len(line) == 0 {
			continue
		}

		prefix := []byte("data:")
		if !bytes.HasPrefix(line, prefix) {
			continue
		}
		data := bytes.TrimSpace(line[len(prefix):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		if !gjson.ValidBytes(data) {
			continue
		}

		parsed := gjson.ParseBytes(data)
		text, reasoning := extractTextFromJSON(parsed)
		if text != "" {
			// reverse scan, prepend to preserve stream order
			contentBuilder.WriteString(text)
			contentBuilder.WriteString("\u0000")
		}
		if reasoning != "" {
			reasoningBuilder.WriteString(reasoning)
			reasoningBuilder.WriteString("\u0000")
		}
	}

	content := reverseNullSeparated(contentBuilder.String())
	reasoning := reverseNullSeparated(reasoningBuilder.String())
	return content, reasoning
}

func extractTextFromJSON(parsed gjson.Result) (string, string) {
	for _, path := range []string{
		"output_text",
		"response.output_text",
		"choices.0.message.content",
		"choices.0.delta.content",
		"response.output.0.content.0.text",
		"output.0.content.0.text",
		"output.0.text",
		"text",
	} {
		if v := parsed.Get(path); v.Exists() && v.String() != "" {
			text := v.String()
			reasoning := ""
			if path == "choices.0.message.content" || path == "choices.0.delta.content" {
				reasoning = firstNonEmptyString(
					parsed.Get("choices.0.message.reasoning_content").String(),
					parsed.Get("choices.0.delta.reasoning_content").String(),
					parsed.Get("choices.0.message.reasoning").String(),
					parsed.Get("choices.0.delta.reasoning").String(),
				)
			}
			return text, reasoning
		}
	}

	reasoning := firstNonEmptyString(
		parsed.Get("choices.0.message.reasoning_content").String(),
		parsed.Get("choices.0.delta.reasoning_content").String(),
		parsed.Get("choices.0.message.reasoning").String(),
		parsed.Get("choices.0.delta.reasoning").String(),
	)
	return "", reasoning
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func reverseNullSeparated(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "\u0000")
	if len(parts) == 0 {
		return ""
	}
	var out strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part == "" {
			continue
		}
		out.WriteString(part)
	}
	return out.String()
}

func truncateText(body []byte, maxBytes int) string {
	if len(body) == 0 {
		return ""
	}
	if maxBytes > 0 && len(body) > maxBytes {
		body = body[:maxBytes]
	}
	return strings.TrimSpace(string(body))
}

func maxContentBytesOrDefault(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func isTextualContentType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if ct == "" {
		return false
	}
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	switch {
	case strings.HasPrefix(ct, "text/"):
		return true
	case strings.Contains(ct, "json"):
		return true
	case strings.Contains(ct, "xml"):
		return true
	case strings.Contains(ct, "event-stream"):
		return true
	case ct == "application/x-www-form-urlencoded":
		return true
	case ct == "application/ndjson":
		return true
	default:
		return false
	}
}

func operationFromPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/v1/chat/completions"),
		strings.HasPrefix(path, "/v1/messages"),
		strings.HasPrefix(path, "/v1/responses"):
		return "chat"
	case strings.HasPrefix(path, "/v1/completions"),
		strings.HasPrefix(path, "/completion"):
		return "completion"
	case strings.HasPrefix(path, "/v1/embeddings"):
		return "embedding"
	case strings.HasPrefix(path, "/reranking"),
		strings.HasPrefix(path, "/rerank"),
		strings.HasPrefix(path, "/v1/rerank"),
		strings.HasPrefix(path, "/v1/reranking"):
		return "rerank"
	case strings.HasPrefix(path, "/infill"):
		return "infill"
	case strings.HasPrefix(path, "/v1/audio/speech"):
		return "speech"
	case strings.HasPrefix(path, "/v1/audio/transcriptions"):
		return "transcription"
	case strings.HasPrefix(path, "/v1/images/generations"):
		return "image_generation"
	case strings.HasPrefix(path, "/v1/images/edits"):
		return "image_edit"
	default:
		return "generation"
	}
}
