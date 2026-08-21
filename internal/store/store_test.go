package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_InsertListAndFilterActivity(t *testing.T) {
	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	for i, model := range []string{"m1", "m2", "m1"} {
		_, err := store.InsertActivity(ctx, ActivityLogEntry{
			Timestamp: time.Unix(int64(100+i), 0),
			Model:     model,
			ReqPath:   "/v1/chat/completions",
			Tokens: TokenMetrics{
				InputTokens:     i + 1,
				OutputTokens:    i + 2,
				PromptPerSecond: float64(10 + i),
				TokensPerSecond: float64(20 + i),
			},
			Metadata: map[string]string{"trace": model},
		})
		if err != nil {
			t.Fatalf("InsertActivity: %v", err)
		}
	}

	page, err := store.ListActivity(ctx, ActivityQuery{Limit: 2, Page: 1})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if page.Total != 3 || page.TotalPages != 2 || len(page.Data) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Data[0].ID <= page.Data[1].ID {
		t.Fatalf("activity is not newest first: %+v", page.Data)
	}
	if page.Data[0].Metadata["trace"] != "m1" {
		t.Fatalf("metadata = %+v", page.Data[0].Metadata)
	}

	filtered, err := store.ListActivity(ctx, ActivityQuery{
		ActivityFilter: ActivityFilter{Models: []string{"m1"}},
		Limit:          10,
		Page:           1,
	})
	if err != nil {
		t.Fatalf("ListActivity filtered: %v", err)
	}
	if filtered.Total != 2 || len(filtered.Data) != 2 {
		t.Fatalf("filtered page = %+v", filtered)
	}
	for _, entry := range filtered.Data {
		if entry.Model != "m1" {
			t.Fatalf("filtered model = %q", entry.Model)
		}
	}
}

// seedFilterActivity inserts 5 rows with ids 1..5 at ts_created 1000..1004,
// alternating models m1/m2/m1/m2/m1, and returns the store.
func seedFilterActivity(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	for i, model := range []string{"m1", "m2", "m1", "m2", "m1"} {
		if _, err := store.InsertActivity(ctx, ActivityLogEntry{
			Timestamp: time.Unix(int64(1000+i), 0),
			Model:     model,
		}); err != nil {
			t.Fatalf("InsertActivity: %v", err)
		}
	}
	return store, ctx
}

// entryIDs returns the ids of a page in the order they were returned.
func entryIDs(entries []ActivityLogEntry) []int {
	ids := make([]int, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func equalIDs(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestStore_ListActivityFilterTimeRange(t *testing.T) {
	store, ctx := seedFilterActivity(t)

	tests := []struct {
		name   string
		filter ActivityFilter
		want   []int
	}{
		{"start only", ActivityFilter{Start: time.Unix(1002, 0)}, []int{5, 4, 3}},
		{"end only", ActivityFilter{End: time.Unix(1001, 0)}, []int{2, 1}},
		{"both bounds inclusive", ActivityFilter{Start: time.Unix(1001, 0), End: time.Unix(1003, 0)}, []int{4, 3, 2}},
		{"empty range", ActivityFilter{Start: time.Unix(9000, 0)}, []int{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := store.ListActivity(ctx, ActivityQuery{ActivityFilter: tt.filter, Limit: 10, Page: 1})
			if err != nil {
				t.Fatalf("ListActivity: %v", err)
			}
			if got := entryIDs(page.Data); !equalIDs(got, tt.want) {
				t.Fatalf("ids = %v, want %v", got, tt.want)
			}
			if page.Total != len(tt.want) {
				t.Fatalf("Total = %d, want %d", page.Total, len(tt.want))
			}
		})
	}
}

func TestStore_ListActivityFilterIDRange(t *testing.T) {
	store, ctx := seedFilterActivity(t)

	tests := []struct {
		name   string
		filter ActivityFilter
		want   []int
	}{
		{"min only", ActivityFilter{MinID: 4}, []int{5, 4}},
		{"max only", ActivityFilter{MaxID: 2}, []int{2, 1}},
		{"both bounds inclusive", ActivityFilter{MinID: 2, MaxID: 4}, []int{4, 3, 2}},
		{"single row", ActivityFilter{MinID: 3, MaxID: 3}, []int{3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := store.ListActivity(ctx, ActivityQuery{ActivityFilter: tt.filter, Limit: 10, Page: 1})
			if err != nil {
				t.Fatalf("ListActivity: %v", err)
			}
			if got := entryIDs(page.Data); !equalIDs(got, tt.want) {
				t.Fatalf("ids = %v, want %v", got, tt.want)
			}
			if page.Total != len(tt.want) {
				t.Fatalf("Total = %d, want %d", page.Total, len(tt.want))
			}
		})
	}
}

func TestStore_ListActivityFilterModels(t *testing.T) {
	store, ctx := seedFilterActivity(t)

	tests := []struct {
		name   string
		models []string
		want   []int
	}{
		{"single", []string{"m1"}, []int{5, 3, 1}},
		{"multiple", []string{"m1", "m2"}, []int{5, 4, 3, 2, 1}},
		{"blank entries ignored", []string{"m2", "  "}, []int{4, 2}},
		{"all blank matches everything", []string{"", " "}, []int{5, 4, 3, 2, 1}},
		{"unknown model", []string{"nope"}, []int{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := store.ListActivity(ctx, ActivityQuery{
				ActivityFilter: ActivityFilter{Models: tt.models},
				Limit:          10,
				Page:           1,
			})
			if err != nil {
				t.Fatalf("ListActivity: %v", err)
			}
			if got := entryIDs(page.Data); !equalIDs(got, tt.want) {
				t.Fatalf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}

// Combined filters must AND together, and Total/TotalPages must describe the
// filtered set rather than the whole table.
func TestStore_ListActivityFilterCombinedPaging(t *testing.T) {
	store, ctx := seedFilterActivity(t)

	page, err := store.ListActivity(ctx, ActivityQuery{
		ActivityFilter: ActivityFilter{
			Models: []string{"m1"},
			Start:  time.Unix(1001, 0),
			MinID:  2,
			MaxID:  5,
		},
		Limit: 1,
		Page:  1,
	})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	// m1 rows are ids 1,3,5; the time and id bounds each drop id 1, leaving
	// 3 and 5.
	if page.Total != 2 || page.TotalPages != 2 {
		t.Fatalf("Total = %d, TotalPages = %d, want 2 and 2", page.Total, page.TotalPages)
	}
	if got := entryIDs(page.Data); !equalIDs(got, []int{5}) {
		t.Fatalf("page 1 ids = %v, want [5]", got)
	}
}

func TestStore_ListActivitySort(t *testing.T) {
	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	// Insert rows whose output_tokens ordering differs from insertion (id) order.
	outputs := []int{30, 10, 20}
	for i, out := range outputs {
		if _, err := store.InsertActivity(ctx, ActivityLogEntry{
			Timestamp: time.Unix(int64(100+i), 0),
			Model:     "m1",
			Tokens:    TokenMetrics{OutputTokens: out},
		}); err != nil {
			t.Fatalf("InsertActivity: %v", err)
		}
	}

	asc, err := store.ListActivity(ctx, ActivityQuery{Limit: 10, Page: 1, Sort: "generated", Order: "asc"})
	if err != nil {
		t.Fatalf("ListActivity asc: %v", err)
	}
	gotAsc := []int{}
	for _, e := range asc.Data {
		gotAsc = append(gotAsc, e.Tokens.OutputTokens)
	}
	if len(gotAsc) != 3 || gotAsc[0] != 10 || gotAsc[1] != 20 || gotAsc[2] != 30 {
		t.Fatalf("ascending generated sort = %v", gotAsc)
	}

	desc, err := store.ListActivity(ctx, ActivityQuery{Limit: 10, Page: 1, Sort: "generated", Order: "desc"})
	if err != nil {
		t.Fatalf("ListActivity desc: %v", err)
	}
	gotDesc := []int{}
	for _, e := range desc.Data {
		gotDesc = append(gotDesc, e.Tokens.OutputTokens)
	}
	if len(gotDesc) != 3 || gotDesc[0] != 30 || gotDesc[1] != 20 || gotDesc[2] != 10 {
		t.Fatalf("descending generated sort = %v", gotDesc)
	}

	// Unknown sort keys fall back to id ordering (newest first).
	fallback, err := store.ListActivity(ctx, ActivityQuery{Limit: 10, Page: 1, Sort: "bogus"})
	if err != nil {
		t.Fatalf("ListActivity fallback: %v", err)
	}
	if fallback.Data[0].ID <= fallback.Data[len(fallback.Data)-1].ID {
		t.Fatalf("fallback sort is not newest first: %+v", fallback.Data)
	}
}

func TestStore_ActivityStats(t *testing.T) {
	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	entries := []ActivityLogEntry{
		{
			Timestamp: time.Unix(1, 0),
			Model:     "m1",
			Tokens: TokenMetrics{
				CachedTokens:    2,
				InputTokens:     10,
				OutputTokens:    20,
				PromptPerSecond: 100,
				TokensPerSecond: 50,
			},
		},
		{
			Timestamp: time.Unix(2, 0),
			Model:     "m1",
			Tokens: TokenMetrics{
				CachedTokens:    -1,
				InputTokens:     5,
				OutputTokens:    8,
				PromptPerSecond: 200,
				TokensPerSecond: 100,
			},
		},
		{
			Timestamp: time.Unix(3, 0),
			Model:     "m2",
			Tokens: TokenMetrics{
				InputTokens:     7,
				OutputTokens:    9,
				PromptPerSecond: 300,
			},
		},
	}
	for _, entry := range entries {
		if _, err := store.InsertActivity(ctx, entry); err != nil {
			t.Fatalf("InsertActivity: %v", err)
		}
	}

	stats, err := store.ActivityStats(ctx, ActivityStatsQuery{Model: "m1"})
	if err != nil {
		t.Fatalf("ActivityStats: %v", err)
	}
	if stats.TotalRequests != 2 || stats.TotalInputTokens != 15 || stats.TotalOutputTokens != 28 || stats.TotalCacheTokens != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.PromptHistogram == nil || stats.GenerationHistogram == nil {
		t.Fatalf("expected histograms: %+v", stats)
	}
}

func TestStore_PruneActivity(t *testing.T) {
	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	for i := 0; i < 5; i++ {
		if _, err := store.InsertActivity(ctx, ActivityLogEntry{Timestamp: time.Unix(int64(i), 0), Model: "m"}); err != nil {
			t.Fatalf("InsertActivity: %v", err)
		}
	}
	if err := store.PruneActivity(ctx, 2); err != nil {
		t.Fatalf("PruneActivity: %v", err)
	}
	page, err := store.ListActivity(ctx, ActivityQuery{Limit: 10, Page: 1})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if page.Total != 2 || len(page.Data) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Data[0].ID != 5 || page.Data[1].ID != 4 {
		t.Fatalf("kept IDs = %+v", page.Data)
	}
}

func TestStore_NewFilePersistsActivity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "llama-swap.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New file store: %v", err)
	}
	if _, err := store.InsertActivity(ctx, ActivityLogEntry{Timestamp: time.Unix(1, 0), Model: "m"}); err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	store, err = New(path)
	if err != nil {
		t.Fatalf("reopen file store: %v", err)
	}
	defer store.Close()
	page, err := store.ListActivity(ctx, ActivityQuery{Limit: 10, Page: 1})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if page.Total != 1 || len(page.Data) != 1 || page.Data[0].Model != "m" {
		t.Fatalf("page = %+v", page)
	}
}

func TestStore_NewFileUsesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llama-swap.sqlite")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New file store: %v", err)
	}
	defer store.Close()

	var mode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}
