package server

import (
	"sort"
	"time"

	"github.com/tidwall/gjson"
)

// metricsExtractor reads one backend engine's metrics from response JSON.
//
// Extractors are stateless between requests: the registry holds factories so
// every response gets a fresh instance. Observe is called for each JSON object
// in the response (the whole body for non-streaming, or each SSE data line),
// then Apply is called once in priority order to build the final entry.
//
// Adding an engine means adding a metrics_engine_<name>.go file that registers
// its factory in init(); no other file needs to change.
type metricsExtractor interface {
	// Name identifies the extractor in registry tests.
	Name() string

	// Priority orders Apply calls. Higher priorities run later and override
	// values reported by lower-priority extractors.
	Priority() int

	// Observe records whatever this extractor understands from obj. It returns
	// true when obj carried a metric block for this engine.
	Observe(obj gjson.Result) bool

	// Apply merges the observed values into acc. It is called once at the end,
	// only when Observe returned true for at least one object. It must only set
	// fields the engine actually reported so lower-priority values survive.
	Apply(acc *metricsAccumulator)
}

var extractorFactories []func() metricsExtractor

func registerMetricsExtractor(factory func() metricsExtractor) {
	extractorFactories = append(extractorFactories, factory)
}

func newMetricsExtractors() []metricsExtractor {
	extractors := make([]metricsExtractor, 0, len(extractorFactories))
	for _, factory := range extractorFactories {
		extractors = append(extractors, factory())
	}
	sort.SliceStable(extractors, func(i, j int) bool {
		return extractors[i].Priority() < extractors[j].Priority()
	})
	return extractors
}

// metricsAccumulator holds the token metrics gathered from one response. Unset
// values use the same sentinels as TokenMetrics: -1 for cached/draft counters
// and rates, so consumers can distinguish "not reported" from zero.
type metricsAccumulator struct {
	inputTokens     int64
	outputTokens    int64
	cachedTokens    int64
	draftTokens     int
	draftAccTokens  int
	promptPerSecond float64
	tokensPerSecond float64
	durationMs      int
}

func newMetricsAccumulator(start time.Time) *metricsAccumulator {
	return &metricsAccumulator{
		cachedTokens:    -1,
		draftTokens:     -1,
		draftAccTokens:  -1,
		promptPerSecond: -1,
		tokensPerSecond: -1,
		durationMs:      int(time.Since(start).Milliseconds()),
	}
}

// setDurationAtLeast raises the recorded duration. Backends report different
// timing windows, so the largest reported value wins over the wall clock.
func (acc *metricsAccumulator) setDurationAtLeast(ms int) {
	if ms > acc.durationMs {
		acc.durationMs = ms
	}
}

func (acc *metricsAccumulator) entry(modelID string) ActivityLogEntry {
	return ActivityLogEntry{
		Timestamp: time.Now(),
		Model:     modelID,
		Tokens: TokenMetrics{
			CachedTokens:    int(acc.cachedTokens),
			DraftTokens:     acc.draftTokens,
			DraftAccTokens:  acc.draftAccTokens,
			InputTokens:     int(acc.inputTokens),
			OutputTokens:    int(acc.outputTokens),
			PromptPerSecond: acc.promptPerSecond,
			TokensPerSecond: acc.tokensPerSecond,
		},
		DurationMs: acc.durationMs,
	}
}

// extractMetrics runs the registered extractors over objects and returns the
// composed entry. found reports whether any extractor recognized a metric
// block; when false the caller records a minimal entry instead.
func extractMetrics(modelID string, start time.Time, objects []gjson.Result) (ActivityLogEntry, bool) {
	acc := newMetricsAccumulator(start)
	found := observeAndApply(newMetricsExtractors(), objects, acc)
	return acc.entry(modelID), found
}

// observeAndApply feeds every object to every extractor, then applies only the
// extractors that recognized at least one block.
func observeAndApply(extractors []metricsExtractor, objects []gjson.Result, acc *metricsAccumulator) bool {
	seen := make([]bool, len(extractors))
	found := false
	for _, obj := range objects {
		for i, extractor := range extractors {
			if extractor.Observe(obj) {
				seen[i] = true
				found = true
			}
		}
	}
	for i, extractor := range extractors {
		if seen[i] {
			extractor.Apply(acc)
		}
	}
	return found
}
