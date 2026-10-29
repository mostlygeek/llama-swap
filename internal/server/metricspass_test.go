package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/process"
)

const testUpstreamMetrics = `
# HELP llama_server_tokens_generated_total Tokens generated
# TYPE llama_server_tokens_generated_total counter
llama_server_tokens_generated_total{model="banana"} 123
llama_server_uptime_seconds 456
# HELP llama_server_requests_total Requests
# TYPE llama_server_requests_total counter
`

// newTestMetricsPass builds a monitor with an injected scrape and loaded checker.
func newTestMetricsPass(t *testing.T, cfg config.Config, loaded func(string) bool) *metricsPassMonitor {
	t.Helper()
	logs := logmon.NewGroup(io.Discard, true, true, true)
	m := newMetricsPassMonitor(cfg, logs.ProxyLogs, loaded)
	m.scrape = func(_ context.Context, _, _ string) (string, error) {
		return testUpstreamMetrics, nil
	}
	return m
}

// newTestMetricsPassMutable builds a monitor whose "loaded" state the test can
// flip, so a scrape can be run while the model is ready and then again after
// it unloads. It returns the monitor and a function that sets the ready state.
func newTestMetricsPassMutable(t *testing.T, cfg config.Config) (*metricsPassMonitor, func(bool)) {
	t.Helper()
	logs := logmon.NewGroup(io.Discard, true, true, true)
	var ready bool
	m := newMetricsPassMonitor(cfg, logs.ProxyLogs, func(string) bool { return ready })
	m.scrape = func(_ context.Context, _, _ string) (string, error) {
		return testUpstreamMetrics, nil
	}
	return m, func(b bool) { ready = b }
}

// errReader is a minimal io.Reader for tests: it yields the given chunks one
// at a time, then returns the given final error. A finalErr of io.EOF
// simulates a complete read; any other error simulates a cut-off body.
type errReader struct {
	chunks   []string
	finalErr error
}

// Read returns the next chunk, or the final error once the chunks are
// exhausted.
func (r *errReader) Read(p []byte) (int, error) {
	if len(r.chunks) > 0 {
		n := copy(p, r.chunks[0])
		r.chunks = r.chunks[1:]
		return n, nil
	}
	return 0, r.finalErr
}

// TestReadCapped_CompleteBody checks a body that ends with io.EOF and is under
// the cap returns the full body and a nil error.
func TestReadCapped_CompleteBody(t *testing.T) {
	r := &errReader{chunks: []string{"hello ", "world"}, finalErr: io.EOF}
	got, err := readCapped(r, 1024)
	if err != nil {
		t.Fatalf("complete read should be nil error, got %v", err)
	}
	if want := "hello world"; got != want {
		t.Fatalf("readCapped = %q, want %q", got, want)
	}
}

// TestReadCapped_TruncatedBodyReturnsError checks a read that ends in a non-EOF
// error (a cut-off body) returns that error instead of a nil one, so the
// caller can reject the partial body.
func TestReadCapped_TruncatedBodyReturnsError(t *testing.T) {
	errReset := errors.New("connection reset")
	r := &errReader{chunks: []string{"part"}, finalErr: errReset}
	got, err := readCapped(r, 1024)
	if err == nil {
		t.Fatal("truncated read must return the error, got nil")
	}
	if !errors.Is(err, errReset) {
		t.Fatalf("readCapped error = %v, want the reader error %v", err, errReset)
	}
	if want := "part"; got != want {
		t.Fatalf("readCapped = %q, want the partial body %q", got, want)
	}
}

// TestReadCapped_BodyOverLimit checks a body larger than the cap is rejected
// with errScrapeBodyTooLarge before it is buffered in full, so a hostile
// upstream cannot exhaust memory.
func TestReadCapped_BodyOverLimit(t *testing.T) {
	r := &errReader{chunks: []string{strings.Repeat("a", 4096)}, finalErr: io.EOF}
	got, err := readCapped(r, 1024)
	if !errors.Is(err, errScrapeBodyTooLarge) {
		t.Fatalf("oversized body must return errScrapeBodyTooLarge, got %v", err)
	}
	if len(got) > 4096 {
		t.Fatalf("should not buffer more than the first chunk before rejecting, got %d bytes", len(got))
	}
}

// TestMetricsPassMonitor_OversizedUpstreamKeepsLastSnapshot checks an upstream
// body that exceeds the cap is rejected and does not replace the previous good
// snapshot.
func TestMetricsPassMonitor_OversizedUpstreamKeepsLastSnapshot(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: "http://localhost:1", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	logs := logmon.NewGroup(io.Discard, true, true, true)
	m := newMetricsPassMonitor(cfg, logs.ProxyLogs, func(string) bool { return true })
	// The real scrape closure uses scrapeMaxBodyBytes; shrink it is not
	// possible, so we inject a scrape that mimics an oversized body by
	// returning the size error directly.
	var calls int
	m.scrape = func(_ context.Context, _, _ string) (string, error) {
		calls++
		if calls == 1 {
			return "good_metric 1\n", nil
		}
		return "", errScrapeBodyTooLarge
	}
	mc := cfg.Models["banana"]

	m.scrapeModel("banana", mc)
	snap, ok := m.snapshot("banana")
	if !ok || snap.raw != "good_metric 1\n" {
		t.Fatalf("first scrape should cache the full body, got ok=%v raw=%q", ok, snap.raw)
	}

	m.scrapeModel("banana", mc)
	snap, _ = m.snapshot("banana")
	if snap.raw != "good_metric 1\n" {
		t.Fatalf("oversized scrape must keep the previous snapshot, got raw=%q", snap.raw)
	}
}

// TestMetricsPassMonitor_FailedScrapeKeepsLastSnapshot checks a scrape that
// fails after a partial body does not replace the previous good snapshot.
func TestMetricsPassMonitor_FailedScrapeKeepsLastSnapshot(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: "http://localhost:1", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	logs := logmon.NewGroup(io.Discard, true, true, true)
	m := newMetricsPassMonitor(cfg, logs.ProxyLogs, func(string) bool { return true })
	var calls int
	m.scrape = func(_ context.Context, _, _ string) (string, error) {
		calls++
		if calls == 1 {
			return "good_metric 1\n", nil
		}
		return "good_met", errors.New("connection reset")
	}
	mc := cfg.Models["banana"]

	m.scrapeModel("banana", mc)
	snap, ok := m.snapshot("banana")
	if !ok || snap.raw != "good_metric 1\n" {
		t.Fatalf("first scrape should cache the full body, got ok=%v raw=%q", ok, snap.raw)
	}

	m.scrapeModel("banana", mc)
	snap, _ = m.snapshot("banana")
	if snap.raw != "good_metric 1\n" {
		t.Fatalf("failed scrape must keep the previous snapshot, got raw=%q", snap.raw)
	}
}

// TestMetricNamePrefix checks model IDs map to valid metric-name prefixes.
func TestMetricNamePrefix(t *testing.T) {
	tests := []struct{ in, want string }{
		{"banana", "banana"},
		{"author/model", "author_model"},
		{"a/b-c.d", "a_b_c_d"},
		{"9lives", "_9lives"},
		{"", "_"},
	}
	for _, tt := range tests {
		if got := metricNamePrefix(tt.in); got != tt.want {
			t.Errorf("metricNamePrefix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestRenderNamespaced checks HELP/TYPE and sample lines are all prefixed, and
// that a metric name that is a prefix of another is not mangled.
func TestRenderNamespaced(t *testing.T) {
	got := renderNamespaced("banana", testUpstreamMetrics)
	for _, want := range []string{
		"# HELP banana_llama_server_tokens_generated_total Tokens generated",
		"# TYPE banana_llama_server_tokens_generated_total counter",
		"banana_llama_server_tokens_generated_total{model=\"banana\"} 123",
		"banana_llama_server_uptime_seconds 456",
		"# HELP banana_llama_server_requests_total Requests",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q;\ngot:\n%s", want, got)
		}
	}
	// The bare uptime metric must not be prefixed twice or leave a dangling.
	if strings.Contains(got, "banana_llama_server_uptime_seconds {") {
		t.Errorf("unexpected braces after bare metric name in:\n%s", got)
	}
}

// TestMetricsPassMonitor_RenderNamespaced checks a model's upstream metrics are
// served namespaced by model name.
func TestMetricsPassMonitor_RenderNamespaced(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: "http://localhost:1", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	m := newTestMetricsPass(t, cfg, func(string) bool { return true })
	m.scrapeModel("banana", cfg.Models["banana"])

	w := httptest.NewRecorder()
	m.Render(w)
	body := w.Body.String()
	if !strings.Contains(body, "banana_llama_server_tokens_generated_total") {
		t.Fatalf("expected namespaced metric in /metrics output, got:\n%s", body)
	}
}

// TestRenderNamespaced_CommentNotRewritten checks a plain comment line passes
// through untouched. Before the fix the sample-name rewrite mangled it into an
// invalid sample, which made Prometheus reject the whole response.
func TestRenderNamespaced_CommentNotRewritten(t *testing.T) {
	got := renderNamespaced("banana", "# generated metrics\n")
	if want := "# generated metrics\n"; got != want {
		t.Errorf("comment line = %q, want %q", got, want)
	}
	if strings.Contains(got, "banana__") {
		t.Errorf("comment must not be namespaced, got:\\n%s", got)
	}
}

// TestRenderNamespaced_TabSample checks an unlabeled sample separated by a tab
// gets the model prefix, and the tab and value survive the rewrite.
func TestRenderNamespaced_TabSample(t *testing.T) {
	got := renderNamespaced("banana", "tokens\t123\n")
	if want := "banana_tokens\t123\n"; got != want {
		t.Errorf("tab sample = %q, want %q", got, want)
	}
}

// TestMetricsPassMonitor_RenderLoadedHidden checks the "loaded" visibility hides
// a model that is not running.
func TestMetricsPassMonitor_RenderLoadedHidden(t *testing.T) {
	cfg := config.Config{
		MetricsPass: config.MetricsPassConfig{Visibility: config.MetricsPassVisibilityLoaded},
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: "http://localhost:1", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	m := newTestMetricsPass(t, cfg, func(string) bool { return false })
	m.scrapeModel("banana", cfg.Models["banana"])

	w := httptest.NewRecorder()
	m.Render(w)
	if w.Body.Len() != 0 {
		t.Fatalf("loaded visibility should hide an unloaded model, got:\n%s", w.Body.String())
	}
}

// TestMetricsPassMonitor_RenderAlwaysKept checks the "always" visibility keeps
// the last successful scrape after the model unloads.
func TestMetricsPassMonitor_RenderAlwaysKept(t *testing.T) {
	cfg := config.Config{
		MetricsPass: config.MetricsPassConfig{Visibility: config.MetricsPassVisibilityAlways},
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: "http://localhost:1", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	m, setReady := newTestMetricsPassMutable(t, cfg)
	mc := cfg.Models["banana"]

	setReady(true)
	m.scrapeModel("banana", mc)
	snap, ok := m.snapshot("banana")
	if !ok || !strings.Contains(snap.raw, "llama_server_tokens_generated_total") {
		t.Fatalf("scrape while ready should cache the body, got ok=%v", ok)
	}

	setReady(false)
	m.scrapeModel("banana", mc)
	snap, _ = m.snapshot("banana")
	if !strings.Contains(snap.raw, "llama_server_tokens_generated_total") {
		t.Fatalf("unloaded model should keep the last successful snapshot, got:\\n%s", snap.raw)
	}

	w := httptest.NewRecorder()
	m.Render(w)
	if !strings.Contains(w.Body.String(), "banana_llama_server_tokens_generated_total") {
		t.Fatalf("always visibility should keep the last scrape, got:\\n%s", w.Body.String())
	}
}

// TestMetricsPassMonitor_SkipsUnloadedModel checks a model that is not ready is
// not scraped at all: no upstream call and no snapshot. This is the shared-port
// safety: a stopped model's proxy port may be owned by another model.
func TestMetricsPassMonitor_SkipsUnloadedModel(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: "http://localhost:1", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	logs := logmon.NewGroup(io.Discard, true, true, true)
	var scrapeCalls int
	m := newMetricsPassMonitor(cfg, logs.ProxyLogs, func(string) bool { return false })
	m.scrape = func(_ context.Context, _, _ string) (string, error) {
		scrapeCalls++
		return testUpstreamMetrics, nil
	}
	m.scrapeModel("banana", cfg.Models["banana"])

	if scrapeCalls != 0 {
		t.Fatalf("a not-ready model must not be scraped, got %d calls", scrapeCalls)
	}
	if _, ok := m.snapshot("banana"); ok {
		t.Fatal("a not-ready model must not produce a snapshot")
	}
}

// TestMetricsPassMonitor_DiscardsResultIfUnloadedDuringScrape checks that when a
// model unloads while its body is being scraped, the (possibly foreign) body is
// discarded and the previous snapshot kept, so another model's metrics are never
// published under the wrong prefix.
func TestMetricsPassMonitor_DiscardsResultIfUnloadedDuringScrape(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: "http://localhost:1", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	logs := logmon.NewGroup(io.Discard, true, true, true)
	var ready bool
	var calls int
	m := newMetricsPassMonitor(cfg, logs.ProxyLogs, func(string) bool { return ready })
	// First scrape: model stays ready, returns the good body. Second scrape:
	// the model unloads part-way through and the body belongs to whatever
	// model now owns the port.
	m.scrape = func(_ context.Context, _, _ string) (string, error) {
		calls++
		if calls == 1 {
			return testUpstreamMetrics, nil
		}
		ready = false
		return "other_model_metric 1\n", nil
	}
	mc := cfg.Models["banana"]

	ready = true
	m.scrapeModel("banana", mc)
	snap, ok := m.snapshot("banana")
	if !ok || !strings.Contains(snap.raw, "llama_server_tokens_generated_total") {
		t.Fatalf("scrape while ready should cache the body, got ok=%v", ok)
	}

	m.scrapeModel("banana", mc)
	snap, _ = m.snapshot("banana")
	if strings.Contains(snap.raw, "other_model_metric") {
		t.Fatalf("foreign body must not be cached, got:\\n%s", snap.raw)
	}
	if !strings.Contains(snap.raw, "llama_server_tokens_generated_total") {
		t.Fatalf("previous snapshot must be kept, got:\\n%s", snap.raw)
	}
}

// TestMetricsPassMonitor_NeverScraped checks a model that has not been scraped
// yet renders nothing.
func TestMetricsPassMonitor_NeverScraped(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: "http://localhost:1", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	m := newTestMetricsPass(t, cfg, func(string) bool { return true })
	// No scrape performed; snapshot is empty.
	w := httptest.NewRecorder()
	m.Render(w)
	if w.Body.Len() != 0 {
		t.Fatalf("expected no output before the first scrape, got:\n%s", w.Body.String())
	}
}

// TestMetricsPassMonitor_RenderTabSample checks an unlabeled sample from an
// upstream scrape separated by a tab gets the model prefix in the rendered
// output.
func TestMetricsPassMonitor_RenderTabSample(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: "http://localhost:1", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	m := newTestMetricsPass(t, cfg, func(string) bool { return true })
	m.scrape = func(_ context.Context, _, _ string) (string, error) {
		return "tokens\t123\n", nil
	}
	m.scrapeModel("banana", cfg.Models["banana"])

	w := httptest.NewRecorder()
	m.Render(w)
	if !strings.Contains(w.Body.String(), "banana_tokens\t123") {
		t.Errorf("expected tab-separated sample to be namespaced, got:\\n%s", w.Body.String())
	}
}

// TestMetricsPassMonitor_NilReceiver checks a nil monitor renders nothing, so
// /metrics works when no model opts in.
func TestMetricsPassMonitor_NilReceiver(t *testing.T) {
	var m *metricsPassMonitor
	w := httptest.NewRecorder()
	m.Render(w)
	if w.Body.Len() != 0 {
		t.Fatalf("nil monitor should render nothing, got:\n%s", w.Body.String())
	}
}

// TestMetricsPassMonitor_RenderCommentPassThrough checks a comment line from an
// upstream scrape survives rendering untouched, so a combined /metrics response
// stays valid for Prometheus.
func TestMetricsPassMonitor_RenderCommentPassThrough(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: "http://localhost:1", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	m := newTestMetricsPass(t, cfg, func(string) bool { return true })
	m.scrape = func(_ context.Context, _, _ string) (string, error) {
		return "# generated metrics\nllama_server_tokens 1\n", nil
	}
	m.scrapeModel("banana", cfg.Models["banana"])

	w := httptest.NewRecorder()
	m.Render(w)
	body := w.Body.String()
	if !strings.Contains(body, "# generated metrics\n") {
		t.Errorf("expected comment to pass through, got:\\n%s", body)
	}
	if strings.Contains(body, "banana__") {
		t.Errorf("comment was namespaced into an invalid sample:\\n%s", body)
	}
	if !strings.Contains(body, "banana_llama_server_tokens 1") {
		t.Errorf("expected namespaced sample alongside the comment, got:\\n%s", body)
	}
}

// TestMetricsPassMonitor_ScrapeUsesDirectOutbound checks the scrape dials the
// configured proxy+path directly (not through a router). A fake upstream serves
// a known body; the monitor must cache it.
func TestMetricsPassMonitor_ScrapeUsesDirectOutbound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			t.Errorf("scrape path = %q, want /metrics", r.URL.Path)
		}
		w.Write([]byte(testUpstreamMetrics))
	}))
	t.Cleanup(upstream.Close)

	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: upstream.URL, MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	logs := logmon.NewGroup(io.Discard, true, true, true)
	// Use the real default scrape (direct outbound HTTP) against the fake
	// upstream by leaving m.scrape unset.
	m := newMetricsPassMonitor(cfg, logs.ProxyLogs, func(string) bool { return true })
	m.scrapeModel("banana", cfg.Models["banana"])

	snap, ok := m.snapshot("banana")
	if !ok || !snap.ok {
		t.Fatalf("expected a successful scrape, got ok=%v", ok)
	}
	if !strings.Contains(snap.raw, "llama_server_tokens_generated_total") {
		t.Fatalf("scraped body missing expected metric:\n%s", snap.raw)
	}
}

// TestMetricsPassMonitor_ScrapeJoinsProxyPath checks the scrape joins the
// metrics path to a proxy that has a path prefix, so a model behind a
// path-based proxy is fetched at <prefix>/metrics, not /metrics.
func TestMetricsPassMonitor_ScrapeJoinsProxyPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/llama/metrics" {
			t.Errorf("scrape path = %q, want /llama/metrics", r.URL.Path)
		}
		w.Write([]byte(testUpstreamMetrics))
	}))
	t.Cleanup(upstream.Close)

	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: upstream.URL + "/llama", MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	logs := logmon.NewGroup(io.Discard, true, true, true)
	// The default scrape closure dials proxy+path directly; the loaded checker
	// reports ready so the scrape runs.
	m := newMetricsPassMonitor(cfg, logs.ProxyLogs, func(string) bool { return true })
	m.scrapeModel("banana", cfg.Models["banana"])

	snap, ok := m.snapshot("banana")
	if !ok || !snap.ok {
		t.Fatalf("expected a successful scrape, got ok=%v", ok)
	}
	if !strings.Contains(snap.raw, "llama_server_tokens_generated_total") {
		t.Fatalf("scraped body missing expected metric:\n%s", snap.raw)
	}
}

// TestAnyModelMetricsPass checks the poller gate: true only when a model has
// metricsPass.path set.
func TestAnyModelMetricsPass(t *testing.T) {
	if anyModelMetricsPass(map[string]config.ModelConfig{}) {
		t.Error("empty models should be false")
	}
	if anyModelMetricsPass(map[string]config.ModelConfig{"a": {}}) {
		t.Error("model without path should be false")
	}
	if !anyModelMetricsPass(map[string]config.ModelConfig{"a": {MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}}}) {
		t.Error("model with path should be true")
	}
}

// TestServer_HandleMetrics_PassesUpstreamMetrics checks /metrics returns both
// llama-swap's own metrics and the model's upstream metrics namespaced by model
// name. The model is served by a fake upstream; a real perf monitor and the
// poller are wired into the server, and the scrape goes straight to the fake
// upstream (never through the router).
func TestServer_HandleMetrics_PassesUpstreamMetrics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testUpstreamMetrics))
	}))
	t.Cleanup(upstream.Close)

	// routerCalls records how many times the model router served a request.
	// Reading /metrics must never do so, because that would reset the model's
	// TTL idle window.
	var routerCalls int
	local := newStubRouter([]string{"banana"}, "ok")
	local.serveHTTP = func(http.ResponseWriter, *http.Request) {
		routerCalls++
	}
	// Mark the model ready so the scrape runs; the loaded checker reads the
	// router's running state.
	local.running = map[string]process.ProcessState{"banana": process.StateReady}

	logs := logmon.NewGroup(io.Discard, true, true, true)
	pm, err := perf.New(config.PerformanceConfig{Every: 5 * time.Second}, logs.ProxyLogs)
	if err != nil {
		t.Fatalf("perf.New: %v", err)
	}

	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"banana": {Proxy: upstream.URL, MetricsPass: config.MetricsPassModelConfig{Path: "/metrics"}},
		},
	}
	s := newTestServerWithConfig(cfg, local, newStubRouter(nil, ""))
	s.perf = pm
	s.metricsPass = newMetricsPassMonitor(cfg, logs.ProxyLogs, func(id string) bool {
		st, ok := s.local.RunningStatus()[id]
		return ok && st.State == process.StateReady
	})
	// Run one scrape against the fake upstream so the cache is populated.
	s.metricsPass.scrapeModel("banana", cfg.Models["banana"])

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(body, "banana_llama_server_tokens_generated_total") {
		t.Errorf("expected namespaced upstream metric in /metrics, got:\n%s", body)
	}
	if routerCalls != 0 {
		t.Errorf("reading /metrics must not route through the model (TTL would reset), got %d router calls", routerCalls)
	}
}

// TestServer_HandleMetrics_NoPassthrough checks /metrics behaves as before when
// no model opts in: no namespaced section is rendered.
func TestServer_HandleMetrics_NoPassthrough(t *testing.T) {
	logs := logmon.NewGroup(io.Discard, true, true, true)
	pm, err := perf.New(config.PerformanceConfig{Every: 5 * time.Second}, logs.ProxyLogs)
	if err != nil {
		t.Fatalf("perf.New: %v", err)
	}
	s := newTestServerWithConfig(config.Config{}, newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.perf = pm

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), "banana_") {
		t.Errorf("no model opted in, so no namespaced section should render:\n%s", w.Body.String())
	}
}
