package sqlite

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mostlygeek/llama-swap/internal/store"
)

// newCaptureStore returns an in-memory store with its capture repository.
func newCaptureStore(t *testing.T) (*Store, store.CaptureRepository) {
	t.Helper()
	st, err := New("", "")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return st, st.Captures()
}

func TestStore_CapturePutGet(t *testing.T) {
	ctx := context.Background()
	_, captures := newCaptureStore(t)

	ts := time.Unix(1750000000, 0)
	require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: "c7", Timestamp: ts, Data: []byte("blob")}))

	got, found, err := captures.Get(ctx, "c7")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "c7", got.ID)
	assert.Equal(t, ts.Unix(), got.Timestamp.Unix())
	assert.Equal(t, []byte("blob"), got.Data)
	assert.EqualValues(t, 4, captures.Size())

	_, found, err = captures.Get(ctx, "c8")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestStore_CapturePutRejectsDuplicateID(t *testing.T) {
	ctx := context.Background()
	_, captures := newCaptureStore(t)

	require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: "a", Data: []byte("first")}))
	err := captures.Put(ctx, store.CaptureEntry{ID: "a", Data: []byte("second")})
	require.ErrorIs(t, err, store.ErrCaptureExists)
	assert.EqualValues(t, 5, captures.Size(), "a rejected put does not count")

	got, found, err := captures.Get(ctx, "a")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []byte("first"), got.Data, "the earlier capture is kept")
}

func TestStore_CaptureHas(t *testing.T) {
	ctx := context.Background()
	_, captures := newCaptureStore(t)

	for _, id := range []string{"id-2", "id-4", "id-600"} {
		require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: id, Data: []byte("x")}))
	}

	empty, err := captures.Has(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// More ids than one batch, so the lookup spans several queries.
	ids := make([]string, 0, 700)
	for id := 1; id <= 700; id++ {
		ids = append(ids, fmt.Sprintf("id-%d", id))
	}
	found, err := captures.Has(ctx, ids)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"id-2": true, "id-4": true, "id-600": true}, found)
}

func TestStore_CaptureDelete(t *testing.T) {
	ctx := context.Background()
	_, captures := newCaptureStore(t)

	require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: "a", Data: make([]byte, 10)}))
	require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: "b", Data: make([]byte, 20)}))
	require.NoError(t, captures.Delete(ctx, "a"))
	require.NoError(t, captures.Delete(ctx, "missing"), "deleting a missing id is not an error")
	assert.EqualValues(t, 20, captures.Size())

	_, found, err := captures.Get(ctx, "a")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestStore_CapturePruneDropsOldestBySize(t *testing.T) {
	ctx := context.Background()
	_, captures := newCaptureStore(t)

	// IDs are written out of string order to show pruning follows write
	// order, not ID order.
	order := []string{"e", "d", "c", "b", "a"}
	for _, id := range order {
		require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: id, Data: make([]byte, 100)}))
	}

	require.NoError(t, captures.Prune(ctx, 0), "zero limit is a no-op")
	require.NoError(t, captures.Prune(ctx, 500), "at the limit nothing is pruned")
	assert.EqualValues(t, 500, captures.Size())

	require.NoError(t, captures.Prune(ctx, 250))
	assert.EqualValues(t, 200, captures.Size())
	found, err := captures.Has(ctx, order)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"b": true, "a": true}, found)
}

func TestStore_CapturesPersistAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	activityPath := filepath.Join(dir, "activity.db")
	capturesPath := filepath.Join(dir, "captures.db")

	captureID := store.NewCaptureID(time.Now())

	first, err := New(activityPath, capturesPath)
	require.NoError(t, err)
	entry, err := first.Activity().Insert(ctx, store.ActivityLogEntry{Model: "m", CaptureID: captureID})
	require.NoError(t, err)
	data := bytes.Repeat([]byte("c"), 64)
	require.NoError(t, first.Captures().Put(ctx, store.CaptureEntry{ID: captureID, Data: data}))
	require.NoError(t, first.Close())

	second, err := New(activityPath, capturesPath)
	require.NoError(t, err)
	defer second.Close()

	page, err := second.Activity().List(ctx, store.ActivityQuery{})
	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	assert.Equal(t, entry.ID, page.Data[0].ID)
	assert.Equal(t, captureID, page.Data[0].CaptureID, "capture_id is stored on the activity row")

	got, found, err := second.Captures().Get(ctx, captureID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, data, got.Data)
	assert.EqualValues(t, 64, second.Captures().Size(), "size is reloaded on open")

	// Captures are in their own file, not in the activity database.
	var tables int
	require.NoError(t, second.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'captures'`).Scan(&tables))
	assert.Zero(t, tables)
}

// The activity and captures files are independent: either can be replaced
// and the store still opens. Rows whose capture is gone just have none.
func TestStore_CapturesIndependentOfActivity(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	capturesPath := filepath.Join(dir, "captures.db")

	first, err := New(filepath.Join(dir, "activity.db"), capturesPath)
	require.NoError(t, err)
	require.NoError(t, first.Captures().Put(ctx, store.CaptureEntry{ID: "orphan", Data: []byte("x")}))
	require.NoError(t, first.Close())

	// A new activity database with the old captures file.
	second, err := New(filepath.Join(dir, "other.db"), capturesPath)
	require.NoError(t, err)
	_, found, err := second.Captures().Get(ctx, "orphan")
	require.NoError(t, err)
	assert.True(t, found)
	require.NoError(t, second.Close())

	// An in-memory activity database with the old captures file.
	third, err := New("", capturesPath)
	require.NoError(t, err)
	require.NoError(t, third.Close())
}
