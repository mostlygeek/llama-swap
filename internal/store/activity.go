package store

import (
	"math"
	"sort"
	"time"
)

// TokenMetrics holds token usage and performance metrics.
type TokenMetrics struct {
	CachedTokens    int     `json:"cache_tokens"`
	DraftTokens     int     `json:"draft_tokens"`
	DraftAccTokens  int     `json:"draft_acc_tokens"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	PromptPerSecond float64 `json:"prompt_per_second"`
	TokensPerSecond float64 `json:"tokens_per_second"`
}

// ActivityLogEntry represents parsed token statistics from llama-server logs.
type ActivityLogEntry struct {
	ID              int               `json:"id"`
	Timestamp       time.Time         `json:"timestamp"`
	Src             string            `json:"src"`
	Model           string            `json:"model"`
	ReqPath         string            `json:"req_path"`
	RespContentType string            `json:"resp_content_type"`
	RespStatusCode  int               `json:"resp_status_code"`
	Tokens          TokenMetrics      `json:"tokens"`
	DurationMs      int               `json:"duration_ms"`
	HasCapture      bool              `json:"has_capture"`
	ErrorMsg        string            `json:"error_msg,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// ActivityFilter narrows which activity rows a query matches. The zero value
// matches every row, and each field is independent: set fields are ANDed.
type ActivityFilter struct {
	Models    []string  // model IN (...); blank entries are ignored, empty matches all models
	Start     time.Time // inclusive lower bound on Timestamp; zero is unbounded
	End       time.Time // inclusive upper bound on Timestamp; zero is unbounded
	MinID     int       // inclusive lower bound on ID; 0 is unbounded
	MaxID     int       // inclusive upper bound on ID; 0 is unbounded
	SrcPrefix string    // literal source prefix; empty matches every source
}

// ActivityQuery selects one page of activity rows.
type ActivityQuery struct {
	ActivityFilter
	Limit int
	Page  int
	Sort  string // sort key from ActivitySortKeys, empty defaults to "id"
	Order string // "asc" or "desc", empty defaults to "desc"
}

// DefaultActivityLimit is the page size used when a query does not set one.
const DefaultActivityLimit = 25

// WithDefaults returns a copy of the query with Page and Limit clamped to
// usable values: Page is at least 1 and Limit defaults to
// DefaultActivityLimit. Repositories apply it before running a query.
func (q ActivityQuery) WithDefaults() ActivityQuery {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Limit < 1 {
		q.Limit = DefaultActivityLimit
	}
	return q
}

// activitySortKeys whitelists the sortable API keys. Keys mirror the UI
// column ids. Each repository maps them onto its own storage columns, so
// user-supplied sort input never reaches a backend as raw column names.
var activitySortKeys = []string{
	"id",
	"time",
	"model",
	"src",
	"req_path",
	"resp_status_code",
	"resp_content_type",
	"cached",
	"prompt",
	"generated",
	"drafted",
	"prompt_speed",
	"gen_speed",
	"duration",
}

// ActivitySortKeys returns the sort keys accepted by ActivityQuery.Sort.
func ActivitySortKeys() []string {
	return append([]string(nil), activitySortKeys...)
}

// ValidActivitySortKey reports whether key is accepted by ActivityQuery.Sort.
func ValidActivitySortKey(key string) bool {
	for _, k := range activitySortKeys {
		if k == key {
			return true
		}
	}
	return false
}

// ActivityPage is one page of a List result.
type ActivityPage struct {
	Data       []ActivityLogEntry `json:"data"`
	Page       int                `json:"page"`
	Limit      int                `json:"limit"`
	Total      int                `json:"total"`
	TotalPages int                `json:"total_pages"`
}

// TotalPages returns how many pages of size limit are needed for total rows.
// It is zero when there are no rows or the limit is not positive.
func TotalPages(total, limit int) int {
	if total == 0 || limit <= 0 {
		return 0
	}
	return int(math.Ceil(float64(total) / float64(limit)))
}

// ActivityStatsQuery narrows a Stats call. An empty Model matches all models.
type ActivityStatsQuery struct {
	Model string
}

// ActivityStats are aggregate totals and speed histograms over activity rows.
type ActivityStats struct {
	TotalRequests       int            `json:"total_requests"`
	TotalInputTokens    int            `json:"total_input_tokens"`
	TotalOutputTokens   int            `json:"total_output_tokens"`
	TotalCacheTokens    int            `json:"total_cache_tokens"`
	PromptHistogram     *HistogramData `json:"prompt_histogram"`
	GenerationHistogram *HistogramData `json:"gen_histogram"`
}

// HistogramData is a binned distribution with percentile summaries.
type HistogramData struct {
	Bins    []int   `json:"bins"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	BinSize float64 `json:"binSize"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
}

// NewHistogram bins values into between 5 and 20 equal-width buckets (Sturges'
// rule) and computes the p50/p95/p99 percentiles. It returns nil for no
// values, and a single bucket with a zero BinSize when every value is equal.
// The input is not modified.
func NewHistogram(values []float64) *HistogramData {
	if len(values) == 0 {
		return nil
	}

	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	minVal := sorted[0]
	maxVal := sorted[len(sorted)-1]

	p50 := percentile(sorted, 50)
	p95 := percentile(sorted, 95)
	p99 := percentile(sorted, 99)

	if minVal == maxVal {
		return &HistogramData{
			Bins:    []int{len(values)},
			Min:     minVal,
			Max:     maxVal,
			BinSize: 0,
			P50:     p50,
			P95:     p95,
			P99:     p99,
		}
	}

	const minBins = 5
	const maxBins = 20
	sturges := int(math.Ceil(math.Log2(float64(len(values))))) + 1
	binCount := min(maxBins, max(minBins, sturges))
	binSize := (maxVal - minVal) / float64(binCount)

	bins := make([]int, binCount)
	for _, value := range values {
		idx := min(int(math.Floor((value-minVal)/binSize)), binCount-1)
		bins[idx]++
	}

	return &HistogramData{
		Bins:    bins,
		Min:     minVal,
		Max:     maxVal,
		BinSize: binSize,
		P50:     p50,
		P95:     p95,
		P99:     p99,
	}
}

// percentile linearly interpolates the p-th percentile of sorted values.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := (p / 100) * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	fraction := rank - float64(lower)
	return sorted[lower] + fraction*(sorted[upper]-sorted[lower])
}
