// Package genaimetrics turns finished requests into OpenTelemetry GenAI
// semantic convention histograms and pushes them to an OTLP/HTTP endpoint.
//
// It is opt-in through the standard OTEL_* environment variables and has no
// dependencies beyond the standard library: the OTLP payload is the JSON
// encoding of the OTLP protobuf messages, which every OTLP/HTTP receiver
// accepts. Histograms are cumulative, so a failed push loses nothing; the
// next one carries the totals.
package genaimetrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/store"
)

// instrument is one histogram with the unit and bucket boundaries advised by
// the GenAI semantic conventions.
type instrument struct {
	name, unit, desc string
	bounds           []float64
}

var (
	durationBounds = []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}

	tokenUsage = &instrument{"gen_ai.client.token.usage", "{token}", "Number of input and output tokens used.",
		[]float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864}}
	requestDuration = &instrument{"gen_ai.server.request.duration", "s", "Generative AI server request duration such as time-to-last byte or last output token.",
		durationBounds}
	timePerOutputToken = &instrument{"gen_ai.server.time_per_output_token", "s", "Time per output token generated after the first token for successful responses.",
		[]float64{0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.2, 0.3, 0.4, 0.5, 0.75, 1.0, 2.5}}

	// instruments fixes the export order.
	instruments = []*instrument{tokenUsage, requestDuration, timePerOutputToken}
)

// seriesKey identifies one histogram data point. extraKey/extraVal carry the
// attribute that only some series have (gen_ai.token.type, error.type).
type seriesKey struct {
	inst                       *instrument
	operation, provider, model string
	extraKey, extraVal         string
}

type histogram struct {
	count   uint64
	sum     float64
	buckets []uint64
}

// Exporter aggregates histograms in memory and pushes them on an interval.
type Exporter struct {
	url      string
	headers  map[string]string
	resource map[string]string
	interval time.Duration
	client   *http.Client
	logger   *logmon.Monitor
	start    time.Time

	mu     sync.Mutex
	series map[seriesKey]*histogram

	stop chan struct{}
	done chan struct{}
}

// FromEnv builds an Exporter from the standard OTel SDK environment
// variables. It returns nil, nil when no OTLP endpoint is set or metrics are
// disabled, so callers pay nothing unless the user opted in.
func FromEnv(getenv func(string) string, version string, logger *logmon.Monitor) (*Exporter, error) {
	if strings.EqualFold(getenv("OTEL_SDK_DISABLED"), "true") || getenv("OTEL_METRICS_EXPORTER") == "none" {
		return nil, nil
	}
	endpoint := getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	if endpoint == "" {
		base := getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		if base == "" {
			return nil, nil
		}
		endpoint = strings.TrimRight(base, "/") + "/v1/metrics"
	}
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return nil, fmt.Errorf("invalid OTLP metrics endpoint %q: %w", endpoint, err)
	}

	protocol := firstNonEmpty(getenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"), getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	if protocol != "" && protocol != "http/json" && logger != nil {
		logger.Warnf("OTLP protocol %q is not supported, sending metrics as http/json", protocol)
	}

	interval, err := envMillis(getenv, "OTEL_METRIC_EXPORT_INTERVAL", 60*time.Second)
	if err != nil {
		return nil, err
	}
	timeout, err := envMillis(getenv, "OTEL_EXPORTER_OTLP_TIMEOUT", 10*time.Second)
	if err != nil {
		return nil, err
	}

	headers, err := parsePairs(firstNonEmpty(getenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS"), getenv("OTEL_EXPORTER_OTLP_HEADERS")))
	if err != nil {
		return nil, fmt.Errorf("invalid OTLP headers: %w", err)
	}
	resource, err := parsePairs(getenv("OTEL_RESOURCE_ATTRIBUTES"))
	if err != nil {
		return nil, fmt.Errorf("invalid OTEL_RESOURCE_ATTRIBUTES: %w", err)
	}
	if name := getenv("OTEL_SERVICE_NAME"); name != "" {
		resource["service.name"] = name
	} else if resource["service.name"] == "" {
		resource["service.name"] = "llama-swap"
	}
	if resource["service.version"] == "" && version != "" {
		resource["service.version"] = version
	}

	return &Exporter{
		url:      endpoint,
		headers:  headers,
		resource: resource,
		interval: interval,
		client:   &http.Client{Timeout: timeout},
		logger:   logger,
		start:    time.Now(),
		series:   make(map[seriesKey]*histogram),
	}, nil
}

// Start begins pushing every interval until Shutdown.
func (e *Exporter) Start() {
	e.stop = make(chan struct{})
	e.done = make(chan struct{})
	if e.logger != nil {
		e.logger.Infof("OTLP GenAI metrics enabled: endpoint=%s interval=%s", e.url, e.interval)
	}
	go func() {
		defer close(e.done)
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		for {
			select {
			case <-e.stop:
				return
			case <-ticker.C:
				e.pushAndLog(context.Background())
			}
		}
	}()
}

// Shutdown stops the push loop and sends one last push so requests finished
// since the previous one are not lost.
func (e *Exporter) Shutdown(ctx context.Context) {
	if e.stop != nil {
		close(e.stop)
		<-e.done
	}
	e.pushAndLog(ctx)
}

func (e *Exporter) pushAndLog(ctx context.Context) {
	if err := e.push(ctx); err != nil && e.logger != nil {
		e.logger.Warnf("OTLP metrics push failed, retrying next interval: %v", err)
	}
}

func (e *Exporter) push(ctx context.Context) error {
	p := e.payload(time.Now())
	if len(p.ResourceMetrics[0].ScopeMetrics[0].Metrics) == 0 {
		return nil // nothing recorded yet
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// Record adds one finished request. upstream is the kind of server that
// answered it, as detected by the router.
func (e *Exporter) Record(entry store.ActivityLogEntry, upstream hint.Upstream) {
	base := seriesKey{
		operation: operationName(entry),
		provider:  providerName(upstream),
		model:     entry.Model,
	}
	failed := entry.RespStatusCode >= 400

	e.mu.Lock()
	defer e.mu.Unlock()

	// Includes loading the model when this request swapped it in.
	duration := base
	duration.inst = requestDuration
	if failed {
		duration.extraKey, duration.extraVal = "error.type", strconv.Itoa(entry.RespStatusCode)
	}
	e.observe(duration, float64(entry.DurationMs)/1000)

	// Zero means unknown here: failed requests carry zeroed token counts.
	for _, t := range []struct {
		kind  string
		count int
	}{{"input", entry.Tokens.InputTokens}, {"output", entry.Tokens.OutputTokens}} {
		if t.count > 0 {
			k := base
			k.inst, k.extraKey, k.extraVal = tokenUsage, "gen_ai.token.type", t.kind
			e.observe(k, float64(t.count))
		}
	}

	// TokensPerSecond is the decode rate (prefill excluded), so its inverse
	// is the time per output token after the first one.
	if !failed && entry.Tokens.TokensPerSecond > 0 {
		k := base
		k.inst = timePerOutputToken
		e.observe(k, 1/entry.Tokens.TokensPerSecond)
	}
}

func (e *Exporter) observe(k seriesKey, v float64) {
	h := e.series[k]
	if h == nil {
		h = &histogram{buckets: make([]uint64, len(k.inst.bounds)+1)}
		e.series[k] = h
	}
	h.count++
	h.sum += v
	// OTel buckets are (previous bound, bound]: the first bound >= v.
	h.buckets[sort.SearchFloat64s(k.inst.bounds, v)]++
}

// operationName maps a request to a gen_ai.operation.name. Tasks without a
// well-known value keep llama-swap's task name (rerank, speech, ...).
func operationName(e store.ActivityLogEntry) string {
	switch hint.Task(e.Task) {
	case hint.TaskChat:
		p := e.ReqPath
		if (strings.HasSuffix(p, "/completions") && !strings.HasSuffix(p, "/chat/completions")) ||
			strings.HasSuffix(p, "/completion") || strings.HasSuffix(p, "/infill") {
			return "text_completion"
		}
		return "chat"
	case hint.TaskEmbedding:
		return "embeddings"
	case hint.TaskRealtime:
		return "realtime_inference"
	case hint.TaskUnknown:
		return "unknown"
	}
	return e.Task
}

// providerName maps the detected upstream to a gen_ai.provider.name. The
// semantic conventions' well-known values (openai, anthropic) match the hint
// names; llama-server is reported under its project name.
func providerName(u hint.Upstream) string {
	switch u {
	case hint.LlamaServer:
		return "llama.cpp"
	case hint.Unknown:
		return "unknown"
	}
	return string(u)
}

// OTLP/JSON messages, limited to the fields used here. 64-bit integers are
// plain JSON numbers, which OTLP/JSON receivers accept.
type (
	otlpKV struct {
		Key   string `json:"key"`
		Value struct {
			StringValue string `json:"stringValue"`
		} `json:"value"`
	}
	otlpPoint struct {
		Attributes        []otlpKV  `json:"attributes"`
		StartTimeUnixNano int64     `json:"startTimeUnixNano"`
		TimeUnixNano      int64     `json:"timeUnixNano"`
		Count             uint64    `json:"count"`
		Sum               float64   `json:"sum"`
		BucketCounts      []uint64  `json:"bucketCounts"`
		ExplicitBounds    []float64 `json:"explicitBounds"`
	}
	otlpMetric struct {
		Name        string `json:"name"`
		Unit        string `json:"unit"`
		Description string `json:"description"`
		Histogram   struct {
			AggregationTemporality int         `json:"aggregationTemporality"`
			DataPoints             []otlpPoint `json:"dataPoints"`
		} `json:"histogram"`
	}
	otlpScope struct {
		Name string `json:"name"`
	}
	otlpScopeMetrics struct {
		Scope   otlpScope    `json:"scope"`
		Metrics []otlpMetric `json:"metrics"`
	}
	otlpResource struct {
		Attributes []otlpKV `json:"attributes"`
	}
	otlpResourceMetrics struct {
		Resource     otlpResource       `json:"resource"`
		ScopeMetrics []otlpScopeMetrics `json:"scopeMetrics"`
	}
	otlpRequest struct {
		ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
	}
)

const cumulative = 2 // AGGREGATION_TEMPORALITY_CUMULATIVE

// payload snapshots every series into an ExportMetricsServiceRequest.
func (e *Exporter) payload(now time.Time) otlpRequest {
	e.mu.Lock()
	byInst := make(map[*instrument][]otlpPoint)
	for k, h := range e.series {
		attrs := []otlpKV{kv("gen_ai.operation.name", k.operation), kv("gen_ai.provider.name", k.provider), kv("gen_ai.request.model", k.model)}
		if k.extraKey != "" {
			attrs = append(attrs, kv(k.extraKey, k.extraVal))
		}
		byInst[k.inst] = append(byInst[k.inst], otlpPoint{
			Attributes:        attrs,
			StartTimeUnixNano: e.start.UnixNano(),
			TimeUnixNano:      now.UnixNano(),
			Count:             h.count,
			Sum:               h.sum,
			BucketCounts:      append([]uint64(nil), h.buckets...),
			ExplicitBounds:    k.inst.bounds,
		})
	}
	e.mu.Unlock()

	var metrics []otlpMetric
	for _, inst := range instruments {
		if len(byInst[inst]) == 0 {
			continue
		}
		m := otlpMetric{Name: inst.name, Unit: inst.unit, Description: inst.desc}
		m.Histogram.AggregationTemporality = cumulative
		m.Histogram.DataPoints = byInst[inst]
		metrics = append(metrics, m)
	}

	var resource otlpResource
	keys := make([]string, 0, len(e.resource))
	for k := range e.resource {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		resource.Attributes = append(resource.Attributes, kv(k, e.resource[k]))
	}
	return otlpRequest{ResourceMetrics: []otlpResourceMetrics{{
		Resource: resource,
		ScopeMetrics: []otlpScopeMetrics{{
			Scope:   otlpScope{Name: "github.com/mostlygeek/llama-swap"},
			Metrics: metrics,
		}},
	}}}
}

func kv(k, v string) otlpKV {
	a := otlpKV{Key: k}
	a.Value.StringValue = v
	return a
}

// parsePairs reads the k1=v1,k2=v2 lists used by OTEL_EXPORTER_OTLP_HEADERS
// and OTEL_RESOURCE_ATTRIBUTES. Values may be percent-encoded.
func parsePairs(s string) (map[string]string, error) {
	out := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		if strings.TrimSpace(pair) == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("%q is not key=value", pair)
		}
		v, err := url.PathUnescape(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("%q: %w", pair, err)
		}
		out[k] = v
	}
	return out, nil
}

func envMillis(getenv func(string) string, name string, def time.Duration) (time.Duration, error) {
	s := getenv(name)
	if s == "" {
		return def, nil
	}
	ms, err := strconv.Atoi(s)
	if err != nil || ms <= 0 {
		return 0, fmt.Errorf("%s must be a positive number of milliseconds, got %q", name, s)
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
