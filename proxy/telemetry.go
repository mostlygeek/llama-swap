package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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

type parsedToolCall struct {
	ID        string
	Name      string
	Type      string
	Arguments string
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
	responseModel, toolCalls := responseDetailsFromBody(metric.RespContentType, respBody)

	spanName := "llama-swap " + route
	startTime := metric.Timestamp
	if startTime.IsZero() {
		startTime = time.Now()
	}
	if metric.DurationMs > 0 {
		startTime = startTime.Add(-time.Duration(metric.DurationMs) * time.Millisecond)
	}

	spanCtx, span := tm.tracer.Start(
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
		if input, ok := structuredChatInputFromBody(reqBody); ok {
			span.SetAttributes(attribute.String("langfuse.observation.input", input))
		} else if text := truncateText(reqBody, tm.maxContentBytes); text != "" {
			span.SetAttributes(attribute.String("gen_ai.prompt", text))
		}
	}

	if tm.captureOutput && len(respBody) > 0 && isTextualContentType(metric.RespContentType) {
		if text, _ := responseTextFromBody(metric.RespContentType, respBody); text != "" {
			span.SetAttributes(attribute.String("gen_ai.completion", truncateText([]byte(text), tm.maxContentBytes)))
		}
	}

	if len(toolCalls) > 0 {
		tm.recordToolCalls(spanCtx, metric, toolCalls, startTime)
	}
}

func metricTimestampOrNow(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Now()
	}
	return ts
}

func responseDetailsFromBody(contentType string, body []byte) (string, []parsedToolCall) {
	if len(body) == 0 {
		return "", nil
	}

	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "event-stream") {
		model := responseModelFromStreamingBody(body)
		_, toolCalls := responseDetailsFromStreamingBody(body)
		return model, toolCalls
	}

	if !gjson.ValidBytes(body) {
		return "", nil
	}

	parsed := gjson.ParseBytes(body)
	return responseModelFromJSON(parsed), toolCallsFromJSON(parsed)
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

	for _, rawLine := range bytes.Split(body, []byte("\n")) {
		line := bytes.TrimSpace(rawLine)
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
			contentBuilder.WriteString(text)
		}
		if reasoning != "" {
			reasoningBuilder.WriteString(reasoning)
		}
	}

	return contentBuilder.String(), reasoningBuilder.String()
}

func responseModelFromStreamingBody(body []byte) string {
	for _, rawLine := range bytes.Split(body, []byte("\n")) {
		line := bytes.TrimSpace(rawLine)
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
		if model := responseModelFromJSON(parsed); model != "" {
			return model
		}
	}

	return ""
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

func responseModelFromJSON(parsed gjson.Result) string {
	if model := parsed.Get("model"); model.Exists() {
		return model.String()
	}
	if model := parsed.Get("response.model"); model.Exists() {
		return model.String()
	}
	return ""
}

func responseDetailsFromStreamingBody(body []byte) (string, []parsedToolCall) {
	var contentBuilder strings.Builder
	builders := make(map[int]*parsedToolCall)

	for _, rawLine := range bytes.Split(body, []byte("\n")) {
		line := bytes.TrimSpace(rawLine)
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
		if text, _ := extractTextFromJSON(parsed); text != "" {
			contentBuilder.WriteString(text)
		}
		mergeToolCallsFromJSON(parsed, builders)
	}

	return contentBuilder.String(), sortedToolCalls(builders)
}

func toolCallsFromJSON(parsed gjson.Result) []parsedToolCall {
	builders := make(map[int]*parsedToolCall)
	mergeToolCallsFromJSON(parsed, builders)
	return sortedToolCalls(builders)
}

func mergeToolCallsFromJSON(parsed gjson.Result, builders map[int]*parsedToolCall) {
	for _, path := range []string{"choices.0.delta.tool_calls", "choices.0.message.tool_calls"} {
		arr := parsed.Get(path)
		if !arr.Exists() || !arr.IsArray() {
			continue
		}

		arr.ForEach(func(_, item gjson.Result) bool {
			index := int(item.Get("index").Int())
			if index < 0 {
				index = 0
			}

			builder := builders[index]
			if builder == nil {
				builder = &parsedToolCall{}
				builders[index] = builder
			}

			if id := item.Get("id").String(); id != "" {
				builder.ID = id
			}
			if toolType := item.Get("type").String(); toolType != "" {
				builder.Type = toolType
			}
			if name := item.Get("function.name").String(); name != "" {
				builder.Name = name
			}
			if args := item.Get("function.arguments").String(); args != "" {
				builder.Arguments += args
			}
			return true
		})
	}
}

func sortedToolCalls(builders map[int]*parsedToolCall) []parsedToolCall {
	if len(builders) == 0 {
		return nil
	}

	indexes := make([]int, 0, len(builders))
	for index := range builders {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	calls := make([]parsedToolCall, 0, len(indexes))
	for _, index := range indexes {
		if builder := builders[index]; builder != nil {
			calls = append(calls, *builder)
		}
	}
	return calls
}

func structuredChatInputFromBody(body []byte) (string, bool) {
	if !gjson.ValidBytes(body) {
		return "", false
	}

	parsed := gjson.ParseBytes(body)
	messages := parsed.Get("messages")
	if !messages.Exists() || !messages.IsArray() {
		return "", false
	}

	structured := make(map[string]any)
	var systemMessages []string
	var userMessages []string
	var assistantMessages []string
	var toolMessages []string
	var allMessages []map[string]string

	messages.ForEach(func(_, message gjson.Result) bool {
		role := strings.TrimSpace(message.Get("role").String())
		content := messageContentFromJSON(message.Get("content"))
		if role == "" && content == "" {
			return true
		}

		if content != "" {
			switch role {
			case "system":
				systemMessages = append(systemMessages, content)
			case "user":
				userMessages = append(userMessages, content)
			case "assistant":
				assistantMessages = append(assistantMessages, content)
			case "tool":
				toolMessages = append(toolMessages, content)
			}
		}

		entry := make(map[string]string, 2)
		if role != "" {
			entry["role"] = role
		}
		if content != "" {
			entry["content"] = content
		}
		if len(entry) > 0 {
			allMessages = append(allMessages, entry)
		}
		return true
	})

	if len(systemMessages) == 0 && len(userMessages) == 0 && len(assistantMessages) == 0 && len(toolMessages) == 0 {
		return "", false
	}

	if len(systemMessages) > 0 {
		structured["system"] = systemMessages
	}
	if len(userMessages) > 0 {
		structured["user"] = userMessages
	}
	if len(assistantMessages) > 0 {
		structured["assistant"] = assistantMessages
	}
	if len(toolMessages) > 0 {
		structured["tool"] = toolMessages
	}
	structured["messages"] = allMessages

	payload, err := json.Marshal(structured)
	if err != nil {
		return "", false
	}
	return string(payload), true
}

func messageContentFromJSON(content gjson.Result) string {
	if !content.Exists() {
		return ""
	}
	if content.Type == gjson.String {
		return content.String()
	}
	if content.Raw != "" {
		return strings.TrimSpace(content.Raw)
	}
	return strings.TrimSpace(content.String())
}

func (tm *TelemetryManager) recordToolCalls(ctx context.Context, metric ActivityLogEntry, toolCalls []parsedToolCall, startTime time.Time) {
	for _, toolCall := range toolCalls {
		name := "execute_tool"
		if toolCall.Name != "" {
			name += " " + toolCall.Name
		}

		_, span := tm.tracer.Start(
			ctx,
			name,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithTimestamp(startTime),
		)
		span.SetAttributes(
			attribute.String("langfuse.observation.type", "tool"),
			attribute.String("gen_ai.operation.name", "execute_tool"),
			attribute.String("gen_ai.tool.name", toolCall.Name),
			attribute.String("gen_ai.tool.type", firstNonEmptyString(toolCall.Type, "function")),
			attribute.String("gen_ai.tool.call.id", toolCall.ID),
			attribute.String("gen_ai.tool.call.arguments", normalizeToolArguments(toolCall.Arguments)),
			attribute.String("langfuse.observation.metadata.route", metric.ReqPath),
			attribute.Int("langfuse.observation.metadata.activity_id", metric.ID),
		)
		span.End(trace.WithTimestamp(startTime))
	}
}

func normalizeToolArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}
	if json.Valid([]byte(arguments)) {
		return arguments
	}
	return arguments
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
