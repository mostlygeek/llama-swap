package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
)

// scrapeTimeout bounds a single upstream metrics scrape. Metrics endpoints are
// local and fast; a hung one must not stall the poller.
const scrapeTimeout = 10 * time.Second

// scrapeMaxBodyBytes caps the size of one upstream metrics body. A real
// /metrics body is small (KB); 10 MiB is far above any legitimate body and far
// below the memory a hostile stream would otherwise consume. A body larger than
// this is rejected before it is cached.
const scrapeMaxBodyBytes = 10 * 1024 * 1024

// errScrapeBodyTooLarge is returned when an upstream metrics body exceeds
// scrapeMaxBodyBytes, so the scrape is rejected and the previous snapshot is
// kept.
var errScrapeBodyTooLarge = errors.New("upstream metrics body exceeds the size cap")

// modelMetricsSnapshot is the last successful upstream scrape for one model.
// A failed scrape leaves the previous snapshot in place, so a flapping
// upstream does not drop data that was already collected.
type modelMetricsSnapshot struct {
	raw       string
	scrapedAt time.Time
	ok        bool
}

// metricsPassMonitor scrapes each configured model's upstream Prometheus
// endpoint on its own cadence and caches the latest body. /metrics renders the
// cached bodies namespaced by model name.
//
// The scrape dials the model's proxy address directly rather than routing a
// request through the local router, for the same reason capcompat does:
// routing a synthetic request through the router would reset the model's TTL
// idle window and occupy a concurrency slot. Because the scrape is a background
// poll decoupled from the client's /metrics request, and the client request
// only reads the cache, reading /metrics never touches a model's lastUse.
type metricsPassMonitor struct {
	cfg   config.Config
	log   *logmon.Monitor
	mu    sync.RWMutex
	snaps map[string]modelMetricsSnapshot

	ctx    context.Context
	cancel context.CancelFunc

	// scrape fetches the upstream metrics body for a model's proxy+path.
	// Injected in tests, which point it at an httptest server.
	scrape func(ctx context.Context, proxy, path string) (string, error)
	// loaded reports whether a model is currently running; used by the
	// "loaded" visibility setting.
	loaded func(modelID string) bool
}

func newMetricsPassMonitor(cfg config.Config, log *logmon.Monitor, loaded func(string) bool) *metricsPassMonitor {
	client := &http.Client{Timeout: scrapeTimeout}
	return &metricsPassMonitor{
		cfg:    cfg,
		log:    log,
		snaps:  make(map[string]modelMetricsSnapshot),
		loaded: loaded,
		scrape: func(ctx context.Context, proxy, path string) (string, error) {
			base, err := url.Parse(proxy)
			if err != nil {
				return "", fmt.Errorf("parse proxy %q: %w", proxy, err)
			}
			// Join rather than assign so a proxy with a path prefix
			// (http://host/llama) keeps it: the metrics endpoint is the
			// proxy's base path plus the metrics path (/llama/metrics).
			base = base.JoinPath(path)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
			if err != nil {
				return "", err
			}
			resp, err := client.Do(req)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return "", fmt.Errorf("upstream returned %s", resp.Status)
			}
			return readCapped(resp.Body, scrapeMaxBodyBytes)
		},
	}
}

// readCapped reads r until it returns a non-nil error, stops reading, and
// returns an error once more than limit bytes would be read. A complete body
// under the cap ends with io.EOF (or a 0-byte read followed by io.EOF), which
// is not an error: the full body is returned with a nil error. A body that
// would exceed the cap is cut off and errScrapeBodyTooLarge is returned so the
// caller rejects the oversized body instead of buffering it. Any other read
// error (a connection reset, an unexpected EOF, or the read timeout after a
// partial body) is also returned.
//
// Inputs: r - the body to read (e.g. a *http.Response body); limit - the
// maximum number of bytes to accept (0 or below means no cap).
// Outputs: the bytes read so far, and the first non-EOF read error or
// errScrapeBodyTooLarge (nil on a complete read under the cap).
func readCapped(r io.Reader, limit int) (string, error) {
	var b strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			if err == io.EOF {
				return b.String(), nil
			}
			return b.String(), err
		}
		if limit > 0 && b.Len() > limit {
			return b.String(), errScrapeBodyTooLarge
		}
	}
}

// configuredModels returns the model IDs whose metricsPass.path is set, sorted.
func (m *metricsPassMonitor) configuredModels() []string {
	ids := make([]string, 0, len(m.cfg.Models))
	for id, mc := range m.cfg.Models {
		if mc.MetricsPass.Enabled() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Start begins one background scrape loop per configured model. It is
// idempotent: a second call does nothing.
func (m *metricsPassMonitor) Start() {
	if m.cancel != nil {
		return
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	for _, id := range m.configuredModels() {
		mc := m.cfg.Models[id]
		go m.scrapeLoop(id, mc)
	}
}

// Stop cancels all scrape loops and waits for them to exit.
func (m *metricsPassMonitor) Stop() {
	if m.cancel == nil {
		return
	}
	m.cancel()
	m.cancel = nil
}

// scrapeLoop scrapes one model immediately, then on its cadence until stopped.
func (m *metricsPassMonitor) scrapeLoop(id string, mc config.ModelConfig) {
	every := mc.MetricsPass.ScrapeEvery(m.cfg.MetricsPass.Every)
	tick := time.NewTicker(every)
	defer tick.Stop()
	m.scrapeModel(id, mc)
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tick.C:
			m.scrapeModel(id, mc)
		}
	}
}

// scrapeModel fetches one model's upstream metrics and caches it on success.
// It scrapes only a model that is currently ready: a stopped model's proxy
// port may be owned by another model, so scraping it would read foreign
// metrics. After a successful scrape it re-checks that the same model is
// still ready; if it unloaded mid-scrape, the body is discarded and the
// previous snapshot kept. A failed scrape keeps the last good body and logs at
// debug. It uses the monitor's context when the poller is running, and a
// fresh background context otherwise, so a one-shot scrape works without
// Start().
func (m *metricsPassMonitor) scrapeModel(id string, mc config.ModelConfig) {
	if !m.modelLoaded(id) {
		return
	}
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	body, err := m.scrape(ctx, mc.Proxy, mc.MetricsPass.Path)
	if err != nil {
		m.log.Debugf("metricsPass: <%s> scrape failed: %v", id, err)
		return
	}
	if !m.modelLoaded(id) {
		// The model unloaded while we were scraping; its proxy port may
		// now belong to another model. Discard the body so foreign
		// metrics are not published under this model's prefix.
		m.log.Debugf("metricsPass: <%s> unloaded during scrape; discarding body", id)
		return
	}
	m.mu.Lock()
	m.snaps[id] = modelMetricsSnapshot{raw: body, scrapedAt: time.Now(), ok: true}
	m.mu.Unlock()
}

// snapshot returns a copy of a model's last scrape, or false when it has never
// succeeded.
func (m *metricsPassMonitor) snapshot(id string) (modelMetricsSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.snaps[id]
	return s, ok
}

// Render writes the namespaced upstream metrics for each model that should be
// shown, in model-ID order. It writes nothing for models that have not been
// scraped yet or are hidden by the visibility setting.
func (m *metricsPassMonitor) Render(w http.ResponseWriter) {
	if m == nil {
		return
	}
	for _, id := range m.configuredModels() {
		if m.cfg.MetricsPass.Visibility == config.MetricsPassVisibilityLoaded && !m.modelLoaded(id) {
			continue
		}
		snap, ok := m.snapshot(id)
		if !ok {
			continue
		}
		_, _ = w.Write([]byte(renderNamespaced(metricNamePrefix(id), snap.raw)))
	}
}

// modelLoaded reports whether a model is currently running, using the injected
// loaded checker. A nil checker reports not-loaded, which is the safe default
// for the "loaded" visibility.
func (m *metricsPassMonitor) modelLoaded(id string) bool {
	if m.loaded == nil {
		return false
	}
	return m.loaded(id)
}

// metricNamePrefix converts a model ID into a valid Prometheus metric-name
// prefix. Any character outside [a-zA-Z0-9_:] becomes "_", so an ID like
// "author/model" becomes "author_model". A leading digit is prefixed with "_"
// so the joined name starts with a valid character.
func metricNamePrefix(modelID string) string {
	var b strings.Builder
	for _, r := range modelID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ':', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		return "_"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "_" + s
	}
	return s
}

// renderNamespaced rewrites a Prometheus text-format body so every metric name
// is prefixed with prefix. HELP and TYPE lines rewrite the metric name; sample
// lines rewrite the name token (everything before the first "{", space, or tab
// or end). Plain "#" comment lines pass through untouched; blank lines are
// dropped.
func renderNamespaced(prefix, raw string) string {
	var out strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if n, ok := directiveLine(line, "# HELP ", prefix); ok {
			out.WriteString(n)
			out.WriteString("\n")
			continue
		}
		if n, ok := directiveLine(line, "# TYPE ", prefix); ok {
			out.WriteString(n)
			out.WriteString("\n")
			continue
		}
		// Any other "#" line is a comment, not a sample. The sample-name
		// rewrite must not touch it: a comment like "# generated metrics"
		// would become "prefix__ generated metrics", which Prometheus
		// rejects as an invalid sample and drops the whole response,
		// including llama-swap's own stats.
		if strings.HasPrefix(line, "#") {
			out.WriteString(line)
			out.WriteString("\n")
			continue
		}
		if idx, nameToken := splitSampleName(line); nameToken != "" {
			out.WriteString(namespacedName(prefix, nameToken))
			out.WriteString(line[idx:])
			out.WriteString("\n")
			continue
		}
		// Unrecognized line; pass through so it is not silently dropped.
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

// splitSampleName returns the index where a sample line's value begins (the
// start of the rest after the name) and the name token. The name is everything
// before the first "{" (labeled sample, e.g. name{a="b"} v), space, or tab
// (unlabeled sample, e.g. name v or name	v). It reports an empty name when
// the line has no name token.
func splitSampleName(line string) (int, string) {
	i := 0
	for i < len(line) && !isNameDelimiter(line[i]) {
		i++
	}
	if i == 0 || i == len(line) {
		return -1, ""
	}
	return i, line[:i]
}

// isNameDelimiter reports whether c separates a sample name from the rest of
// the line. Space and tab both separate unlabeled samples; "{" opens the
// label set of a labeled sample.
func isNameDelimiter(c byte) bool {
	return c == ' ' || c == '	' || c == '{'
}

// directiveLine rewrites a "# HELP name text" or "# TYPE name type" line,
// namespacing the metric name (the token after the directive) with prefix. It
// reports false when the line does not carry a metric name.
func directiveLine(line, directive, prefix string) (string, bool) {
	if !strings.HasPrefix(line, directive) {
		return "", false
	}
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 3 {
		return "", false
	}
	// directive already ends in a space, so only one separator is added.
	out := directive + namespacedName(prefix, parts[2])
	if len(parts) == 4 {
		out += " " + parts[3]
	}
	return out, true
}

// namespacedName returns prefix + "_" + name with every invalid character in
// name replaced by "_". prefix is already a sanitized model name from
// metricNamePrefix. A leading digit would make the joined name invalid, but
// metricNamePrefix never yields a leading digit, so this is safe as written.
func namespacedName(prefix, name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ':', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return prefix + "_" + b.String()
}
