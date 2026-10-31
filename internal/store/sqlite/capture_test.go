package sqlite

import (
	"bytes"
	"context"
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
	require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: 7, Timestamp: ts, Data: []byte("blob")}))

	got, found, err := captures.Get(ctx, 7)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 7, got.ID)
	assert.Equal(t, ts.Unix(), got.Timestamp.Unix())
	assert.Equal(t, []byte("blob"), got.Data)
	assert.EqualValues(t, 4, captures.Size())

	_, found, err = captures.Get(ctx, 8)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestStore_CapturePutReplacesAndTracksSize(t *testing.T) {
	ctx := context.Background()
	_, captures := newCaptureStore(t)

	require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: 1, Data: make([]byte, 100)}))
	require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: 1, Data: make([]byte, 30)}))
	assert.EqualValues(t, 30, captures.Size())

	got, found, err := captures.Get(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	assert.Len(t, got.Data, 30)
}

func TestStore_CaptureHas(t *testing.T) {
	ctx := context.Background()
	_, captures := newCaptureStore(t)

	for _, id := range []int{2, 4, 600} {
		require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: id, Data: []byte("x")}))
	}

	empty, err := captures.Has(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// More ids than one batch, so the lookup spans several queries.
	ids := make([]int, 0, 700)
	for id := 1; id <= 700; id++ {
		ids = append(ids, id)
	}
	found, err := captures.Has(ctx, ids)
	require.NoError(t, err)
	assert.Equal(t, map[int]bool{2: true, 4: true, 600: true}, found)
}

func TestStore_CaptureDelete(t *testing.T) {
	ctx := context.Background()
	_, captures := newCaptureStore(t)

	require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: 1, Data: make([]byte, 10)}))
	require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: 2, Data: make([]byte, 20)}))
	require.NoError(t, captures.Delete(ctx, 1))
	require.NoError(t, captures.Delete(ctx, 99), "deleting a missing id is not an error")
	assert.EqualValues(t, 20, captures.Size())

	_, found, err := captures.Get(ctx, 1)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestStore_CapturePruneDropsOldestBySize(t *testing.T) {
	ctx := context.Background()
	_, captures := newCaptureStore(t)

	for id := 1; id <= 5; id++ {
		require.NoError(t, captures.Put(ctx, store.CaptureEntry{ID: id, Data: make([]byte, 100)}))
	}

	require.NoError(t, captures.Prune(ctx, 0), "zero limit is a no-op")
	require.NoError(t, captures.Prune(ctx, 500), "at the limit nothing is pruned")
	assert.EqualValues(t, 500, captures.Size())

	require.NoError(t, captures.Prune(ctx, 250))
	assert.EqualValues(t, 200, captures.Size())
	found, err := captures.Has(ctx, []int{1, 2, 3, 4, 5})
	require.NoError(t, err)
	assert.Equal(t, map[int]bool{4: true, 5: true}, found)
}

func TestStore_CapturesPersistAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	activityPath := filepath.Join(dir, "activity.db")
	capturesPath := filepath.Join(dir, "captures.db")

	first, err := New(activityPath, capturesPath)
	require.NoError(t, err)
	entry, err := first.Activity().Insert(ctx, store.ActivityLogEntry{Model: "m", Timestamp: time.Unix(1750000000, 0)})
	require.NoError(t, err)
	data := bytes.Repeat([]byte("c"), 64)
	require.NoError(t, first.Captures().Put(ctx, store.CaptureEntry{ID: entry.ID, Timestamp: entry.Timestamp, Data: data}))
	require.NoError(t, first.Close())

	second, err := New(activityPath, capturesPath)
	require.NoError(t, err)
	defer second.Close()

	got, found, err := second.Captures().Get(ctx, entry.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, data, got.Data)
	assert.EqualValues(t, 64, second.Captures().Size(), "size is reloaded on open")

	// Captures are in their own file, not in the activity database.
	var tables int
	require.NoError(t, second.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'captures'`).Scan(&tables))
	assert.Zero(t, tables)
}

func TestStore_CapturesOutOfSyncFailsOpen(t *testing.T) {
	ctx := context.Background()

	// seed writes one activity row and its capture to fresh files and
	// returns their paths.
	seed := func(t *testing.T) (activityPath, capturesPath string) {
		t.Helper()
		dir := t.TempDir()
		activityPath = filepath.Join(dir, "activity.db")
		capturesPath = filepath.Join(dir, "captures.db")
		st, err := New(activityPath, capturesPath)
		require.NoError(t, err)
		entry, err := st.Activity().Insert(ctx, store.ActivityLogEntry{Model: "m", Timestamp: time.Unix(1750000000, 0)})
		require.NoError(t, err)
		require.NoError(t, st.Captures().Put(ctx, store.CaptureEntry{ID: entry.ID, Timestamp: entry.Timestamp, Data: []byte("x")}))
		require.NoError(t, st.Close())
		return activityPath, capturesPath
	}

	t.Run("new activity database", func(t *testing.T) {
		_, capturesPath := seed(t)
		_, err := New(filepath.Join(t.TempDir(), "other.db"), capturesPath)
		require.ErrorIs(t, err, ErrCapturesOutOfSync)
		assert.Contains(t, err.Error(), "no matching activity row")
	})

	t.Run("in-memory activity database", func(t *testing.T) {
		_, capturesPath := seed(t)
		_, err := New("", capturesPath)
		require.ErrorIs(t, err, ErrCapturesOutOfSync)
	})

	t.Run("different activity row", func(t *testing.T) {
		activityPath, capturesPath := seed(t)
		other, err := New(activityPath, "")
		require.NoError(t, err)
		_, err = other.db.Exec(`UPDATE activity SET ts_created = ts_created + 1`)
		require.NoError(t, err)
		require.NoError(t, other.Close())

		_, err = New(activityPath, capturesPath)
		require.ErrorIs(t, err, ErrCapturesOutOfSync)
		assert.Contains(t, err.Error(), "was recorded at")
	})

	t.Run("in sync", func(t *testing.T) {
		activityPath, capturesPath := seed(t)
		st, err := New(activityPath, capturesPath)
		require.NoError(t, err)
		require.NoError(t, st.Close())
	})
}
