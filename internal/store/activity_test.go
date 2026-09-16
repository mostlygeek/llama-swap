package store

import (
	"math"
	"testing"
)

func near(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

func TestActivityQuery_WithDefaults(t *testing.T) {
	tests := []struct {
		name      string
		in        ActivityQuery
		wantPage  int
		wantLimit int
	}{
		{"zero value", ActivityQuery{}, 1, DefaultActivityLimit},
		{"negative", ActivityQuery{Page: -3, Limit: -1}, 1, DefaultActivityLimit},
		{"set values kept", ActivityQuery{Page: 4, Limit: 7}, 4, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.WithDefaults()
			if got.Page != tt.wantPage || got.Limit != tt.wantLimit {
				t.Fatalf("WithDefaults() = page %d limit %d, want %d and %d", got.Page, got.Limit, tt.wantPage, tt.wantLimit)
			}
		})
	}

	// Filter and sort fields must pass through untouched.
	in := ActivityQuery{ActivityFilter: ActivityFilter{Models: []string{"m"}, MinID: 2}, Sort: "time", Order: "asc"}
	got := in.WithDefaults()
	if got.Sort != "time" || got.Order != "asc" || got.MinID != 2 || len(got.Models) != 1 {
		t.Fatalf("WithDefaults() dropped fields: %+v", got)
	}
}

func TestActivity_SortKeys(t *testing.T) {
	keys := ActivitySortKeys()
	if len(keys) == 0 {
		t.Fatal("no sort keys")
	}
	for _, key := range keys {
		if !ValidActivitySortKey(key) {
			t.Fatalf("ValidActivitySortKey(%q) = false", key)
		}
	}
	for _, key := range []string{"", "bogus", "ID", "ts_created; DROP TABLE activity"} {
		if ValidActivitySortKey(key) {
			t.Fatalf("ValidActivitySortKey(%q) = true", key)
		}
	}

	// The returned slice is a copy: mutating it must not change the whitelist.
	keys[0] = "mutated"
	if ValidActivitySortKey("mutated") {
		t.Fatal("ActivitySortKeys() exposes the internal whitelist")
	}
}

func TestActivity_TotalPages(t *testing.T) {
	tests := []struct {
		total, limit, want int
	}{
		{0, 10, 0},
		{5, 0, 0},
		{5, -1, 0},
		{1, 10, 1},
		{10, 10, 1},
		{11, 10, 2},
		{25, 10, 3},
	}
	for _, tt := range tests {
		if got := TotalPages(tt.total, tt.limit); got != tt.want {
			t.Errorf("TotalPages(%d, %d) = %d, want %d", tt.total, tt.limit, got, tt.want)
		}
	}
}

func TestActivity_NewHistogram(t *testing.T) {
	if got := NewHistogram(nil); got != nil {
		t.Fatalf("NewHistogram(nil) = %+v, want nil", got)
	}

	same := NewHistogram([]float64{7, 7, 7})
	if same == nil || len(same.Bins) != 1 || same.Bins[0] != 3 || same.BinSize != 0 || same.Min != 7 || same.Max != 7 {
		t.Fatalf("all-equal histogram = %+v", same)
	}
	if same.P50 != 7 || same.P95 != 7 || same.P99 != 7 {
		t.Fatalf("all-equal percentiles = %+v", same)
	}

	values := []float64{30, 10, 20, 40}
	h := NewHistogram(values)
	if h == nil {
		t.Fatal("nil histogram")
	}
	// Sturges' rule gives 3 bins for 4 values; the minimum is 5.
	if len(h.Bins) != 5 {
		t.Fatalf("bins = %v, want 5 buckets", h.Bins)
	}
	if h.Min != 10 || h.Max != 40 || h.BinSize != 6 {
		t.Fatalf("range = min %v max %v size %v", h.Min, h.Max, h.BinSize)
	}
	count := 0
	for _, n := range h.Bins {
		count += n
	}
	if count != len(values) {
		t.Fatalf("bins %v hold %d values, want %d", h.Bins, count, len(values))
	}
	// The max value lands in the last bucket instead of overflowing.
	if h.Bins[len(h.Bins)-1] != 1 {
		t.Fatalf("max value not in last bucket: %v", h.Bins)
	}
	// Linear interpolation: p50 of 10,20,30,40 is 25.
	if !near(h.P50, 25) || !near(h.P95, 38.5) || !near(h.P99, 39.7) {
		t.Fatalf("percentiles = p50 %v p95 %v p99 %v", h.P50, h.P95, h.P99)
	}
	// Input must not be sorted in place.
	if values[0] != 30 {
		t.Fatalf("input was modified: %v", values)
	}
}
