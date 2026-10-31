package sqlite

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/pressly/goose/v3"
)

// Every sort key the store package accepts must map to a SQL column here, and
// nothing outside that whitelist may be sortable.
func TestActivity_SortColumnsCoverStoreKeys(t *testing.T) {
	keys := store.ActivitySortKeys()
	for _, key := range keys {
		if _, ok := activitySortColumns[key]; !ok {
			t.Errorf("store sort key %q has no SQL column", key)
		}
	}
	if len(activitySortColumns) != len(keys) {
		t.Errorf("activitySortColumns has %d entries, store has %d keys", len(activitySortColumns), len(keys))
	}
	for key := range activitySortColumns {
		if !store.ValidActivitySortKey(key) {
			t.Errorf("SQL sort column key %q is not a store sort key", key)
		}
	}
}

func TestStore_ActivityTaskRouteRoundTrip(t *testing.T) {
	st, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	in := store.ActivityLogEntry{
		Timestamp: time.Unix(100, 0),
		Model:     "m",
		ReqPath:   "/v1/chat/completions",
		Task:      "chat",
		Route:     "peer",
		PeerID:    "cloud",
		Tokens:    store.TokenMetrics{InputTokens: 3, OutputTokens: 9, ReasoningTokens: 6},
	}
	if _, err := st.Activity().Insert(ctx, in); err != nil {
		t.Fatal(err)
	}
	page, err := st.Activity().List(ctx, store.ActivityQuery{Limit: 10, Page: 1})
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("list = %+v, %v", page, err)
	}
	got := page.Data[0]
	if got.Task != "chat" || got.Route != "peer" || got.PeerID != "cloud" || got.Tokens.ReasoningTokens != 6 {
		t.Errorf("entry = %+v", got)
	}
}

// TestStore_ActivityTaskBackfill checks migration 00004 labels rows recorded
// before the task column existed, and leaves reasoning tokens unknown.
func TestStore_ActivityTaskBackfill(t *testing.T) {
	st, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	migrations, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, st.db, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 3); err != nil {
		t.Fatalf("down to 3: %v", err)
	}
	for _, path := range []string{"/v1/chat/completions", "/v1/embeddings", "/v1/audio/speech", "/upstream/m/props"} {
		if _, err := st.db.ExecContext(ctx, `INSERT INTO activity (ts_created, model_id, req_path) VALUES (1, 'm', ?)`, path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	page, err := st.Activity().List(ctx, store.ActivityQuery{Limit: 10, Page: 1, Sort: "id", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"/v1/chat/completions": "chat",
		"/v1/embeddings":       "embedding",
		"/v1/audio/speech":     "speech",
		"/upstream/m/props":    "",
	}
	if len(page.Data) != len(want) {
		t.Fatalf("got %d rows, want %d", len(page.Data), len(want))
	}
	for _, e := range page.Data {
		if e.Task != want[e.ReqPath] {
			t.Errorf("%s task = %q, want %q", e.ReqPath, e.Task, want[e.ReqPath])
		}
		if e.Tokens.ReasoningTokens != -1 {
			t.Errorf("%s reasoning = %d, want -1", e.ReqPath, e.Tokens.ReasoningTokens)
		}
	}
}
