package server

import (
	"context"
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
	m := newTestMetricsPass(t, cfg, func(string) bool { return false })
	m.scrapeModel("banana", cfg.Models["banana"])

	w := httptest.NewRecorder()
	m.Render(w)
	if !strings.Contains(w.Body.String(), "banana_llama_server_tokens_generated_total") {
		t.Fatalf("always visibility should keep the last scrape, got:\n%s", w.Body.String())
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
