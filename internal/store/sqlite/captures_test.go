package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mostlygeek/llama-swap/internal/store"
)

// newCaptureStore opens a store whose captures live in their own file, so the
// tests exercise the same two-database layout the binary uses.
func newCaptureStore(t *testing.T, maxBytes, maxItemBytes int64) (*Store, string) {
	t.Helper()

	dir := t.TempDir()
	st, err := New(Options{
		Path:                 filepath.Join(dir, "activity.db"),
		CapturesPath:         filepath.Join(dir, "captures.db"),
		CapturesMaxBytes:     maxBytes,
		CapturesMaxItemBytes: maxItemBytes,
	})
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	return st, filepath.Join(dir, "captures.db")
}

// openRaw reads a database file directly, for assertions about the schema and
// about state the public API does not expose.
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	return db
}

// diskBytes is the space the captures database occupies: the file plus any WAL
// not yet checkpointed into it. It is what the size budget is measured against.
func diskBytes(t *testing.T, path string) int64 {
	t.Helper()

	var total int64
	for _, p := range []string{path, path + "-wal"} {
		info, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("stat %s: %v", p, err)
		}
		total += info.Size()
	}
	return total
}

func rowCount(t *testing.T, path string) int {
	t.Helper()

	db := openRaw(t, path)
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM captures`).Scan(&count))

	return count
}

func TestStore_CapturesDisabledWithoutPath(t *testing.T) {
	st, err := New(Options{Path: filepath.Join(t.TempDir(), "activity.db")})
	require.NoError(t, err)
	defer st.Close()

	if st.Captures() != nil {
		t.Fatal("Captures() must be nil when no captures file is configured")
	}
}

func TestStore_CapturesPutGetRoundtrip(t *testing.T) {
	ctx := context.Background()
	st, _ := newCaptureStore(t, 1<<20, 1<<20)

	require.NoError(t, st.Captures().Put(ctx, 7, []byte("compressed-blob")))

	got, found, err := st.Captures().Get(ctx, 7)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []byte("compressed-blob"), got)

	got, found, err = st.Captures().Get(ctx, 999)
	require.NoError(t, err)
	require.False(t, found, "an id with no capture must report not found")
	assert.Nil(t, got)
}

func TestStore_CapturesPutReplaces(t *testing.T) {
	ctx := context.Background()
	st, path := newCaptureStore(t, 1<<20, 1<<20)

	require.NoError(t, st.Captures().Put(ctx, 1, []byte("first")))
	require.NoError(t, st.Captures().Put(ctx, 1, []byte("second-is-longer")))

	got, found, err := st.Captures().Get(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []byte("second-is-longer"), got)
	assert.Equal(t, 1, rowCount(t, path), "a repeated id must not accumulate rows")
}

func TestStore_CapturesHas(t *testing.T) {
	ctx := context.Background()
	st, _ := newCaptureStore(t, 1<<20, 1<<20)

	for _, id := range []int{2, 4, 6} {
		require.NoError(t, st.Captures().Put(ctx, id, []byte("x")))
	}

	found, err := st.Captures().Has(ctx, []int{2, 3, 4, 5, 6})
	require.NoError(t, err)
	assert.Equal(t, map[int]bool{2: true, 4: true, 6: true}, found)

	found, err = st.Captures().Has(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, found)
}

func TestStore_CapturesHasLargeBatch(t *testing.T) {
	ctx := context.Background()
	st, _ := newCaptureStore(t, 1<<20, 1<<20)

	// A page larger than one Has batch must still report every stored id: the
	// lookup is split, not truncated.
	const count = hasBatchIDs + 100
	for id := 1; id <= count; id++ {
		require.NoError(t, st.Captures().Put(ctx, id, []byte("x")))
	}

	ids := make([]int, 0, count+1)
	for id := 1; id <= count+1; id++ {
		ids = append(ids, id)
	}

	found, err := st.Captures().Has(ctx, ids)
	require.NoError(t, err)
	require.Len(t, found, count)
	assert.True(t, found[1] && found[count])
	assert.NotContains(t, found, count+1)
}

func TestStore_CapturesDelete(t *testing.T) {
	ctx := context.Background()
	st, path := newCaptureStore(t, 1<<20, 1<<20)

	require.NoError(t, st.Captures().Put(ctx, 1, []byte("aaaaaaaaaa")))
	require.NoError(t, st.Captures().Put(ctx, 2, []byte("bbbbbbbbbb")))
	require.NoError(t, st.Captures().Delete(ctx, 1))

	_, found, err := st.Captures().Get(ctx, 1)
	require.NoError(t, err)
	require.False(t, found)
	assert.Equal(t, 1, rowCount(t, path))

	// Deleting an id that holds nothing is not an error.
	require.NoError(t, st.Captures().Delete(ctx, 1))
}

func TestStore_CapturesRejectOversizedCapture(t *testing.T) {
	ctx := context.Background()
	st, path := newCaptureStore(t, 100_000, 100)

	require.NoError(t, st.Captures().Put(ctx, 1, []byte(blobOf(50))))

	err := st.Captures().Put(ctx, 2, []byte(blobOf(101)))
	require.Error(t, err)
	require.True(t, errors.Is(err, store.ErrCaptureTooLarge), "got %v", err)

	// The rejected capture must not have evicted anything to make room.
	_, found, err := st.Captures().Get(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 1, rowCount(t, path))
}

// At the cap, every write must free exactly what the new blob needs. If pages
// were not handed back after evicting, the file would sit at its high-water
// mark, every later write would read itself as over budget, and the row count
// would fall to zero.
func TestStore_CapturesEvictsOneOldestPerWriteAtTheCap(t *testing.T) {
	const (
		blobSize = 10_000
		budget   = 200_000
	)

	ctx := context.Background()
	st, path := newCaptureStore(t, budget, blobSize)

	for id := 1; id <= 20; id++ {
		require.NoError(t, st.Captures().Put(ctx, id, []byte(blobOf(blobSize))))
	}

	stable := rowCount(t, path)
	require.Greater(t, stable, 5, "budget should keep a useful history")
	require.Less(t, stable, 25, "budget should be enforced")
	t.Logf("steady state: %d captures of %d bytes, %d bytes on disk",
		stable, blobSize, diskBytes(t, path))

	for id := 21; id <= 30; id++ {
		require.NoError(t, st.Captures().Put(ctx, id, []byte(blobOf(blobSize))))
		require.Equal(t, stable, rowCount(t, path), "write %d changed the row count", id)
	}

	assert.LessOrEqual(t, diskBytes(t, path), int64(budget))

	// The newest captures survive; the evicted ones are the oldest.
	found, err := st.Captures().Has(ctx, []int{20, 21, 30})
	require.NoError(t, err)
	assert.Len(t, found, 3, "the newest captures must survive the cap")
}

func TestStore_CapturesEnforcesLoweredBudgetOnOpen(t *testing.T) {
	const blobSize = 10_000
	ctx := context.Background()

	dir := t.TempDir()
	activityPath := filepath.Join(dir, "activity.db")
	capturesPath := filepath.Join(dir, "captures.db")

	first, err := New(Options{
		Path:                 activityPath,
		CapturesPath:         capturesPath,
		CapturesMaxBytes:     400_000,
		CapturesMaxItemBytes: blobSize,
	})
	require.NoError(t, err)
	for id := 1; id <= 20; id++ {
		require.NoError(t, first.Captures().Put(ctx, id, []byte(blobOf(blobSize))))
	}
	require.Equal(t, 20, rowCount(t, capturesPath))
	require.NoError(t, first.Close())

	// maxSizeMB lowered in config while the process was not running.
	second, err := New(Options{
		Path:                 activityPath,
		CapturesPath:         capturesPath,
		CapturesMaxBytes:     200_000,
		CapturesMaxItemBytes: blobSize,
	})
	require.NoError(t, err)
	defer second.Close()

	count := rowCount(t, capturesPath)
	require.Greater(t, count, 5)
	require.Less(t, count, 20)
	assert.LessOrEqual(t, diskBytes(t, capturesPath), int64(200_000))

	// Eviction is oldest-first, so the newest captures are the ones kept.
	found, err := second.Captures().Has(ctx, []int{1, 20})
	require.NoError(t, err)
	assert.NotContains(t, found, 1, "the oldest capture should be evicted")
	assert.Contains(t, found, 20, "the newest capture should survive")
}

func TestStore_CapturesPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	opts := Options{
		Path:                 filepath.Join(dir, "activity.db"),
		CapturesPath:         filepath.Join(dir, "captures.db"),
		CapturesMaxBytes:     1 << 20,
		CapturesMaxItemBytes: 1 << 20,
	}

	first, err := New(opts)
	require.NoError(t, err)
	require.NoError(t, first.Captures().Put(ctx, 11, []byte("persisted")))
	require.NoError(t, first.Close())

	second, err := New(opts)
	require.NoError(t, err)
	defer second.Close()

	got, found, err := second.Captures().Get(ctx, 11)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []byte("persisted"), got)
}

func TestStore_CapturesUseTheirOwnDatabaseFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	activityPath := filepath.Join(dir, "activity.db")
	capturesPath := filepath.Join(dir, "captures.db")

	st, err := New(Options{
		Path:                 activityPath,
		CapturesPath:         capturesPath,
		CapturesMaxBytes:     1 << 20,
		CapturesMaxItemBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.NoError(t, st.Captures().Put(ctx, 1, []byte("x")))
	require.NoError(t, st.Close())

	// Each file holds only its own tables: the capture database is a small
	// standalone blob store that can be deleted without touching activity.
	capturesTables := tableNames(t, capturesPath)
	assert.Contains(t, capturesTables, "captures")
	assert.NotContains(t, capturesTables, "activity")
	assert.NotContains(t, capturesTables, "cache")

	activityTables := tableNames(t, activityPath)
	assert.Contains(t, activityTables, "activity")
	assert.NotContains(t, activityTables, "captures")
}

// Incremental autovacuum is what lets the repository shrink the file after
// evicting. SQLite only accepts the pragma before the file header exists, so
// the captures database must request it ahead of journal_mode.
func TestStore_CapturesDatabaseUsesIncrementalAutoVacuum(t *testing.T) {
	dir := t.TempDir()
	activityPath := filepath.Join(dir, "activity.db")
	capturesPath := filepath.Join(dir, "captures.db")

	st, err := New(Options{
		Path:                 activityPath,
		CapturesPath:         capturesPath,
		CapturesMaxBytes:     1 << 20,
		CapturesMaxItemBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.NoError(t, st.Close())

	assert.Equal(t, int64(2), readInt(t, capturesPath, `PRAGMA auto_vacuum`),
		"captures need auto_vacuum=INCREMENTAL to release pages")
	assert.Equal(t, int64(0), readInt(t, activityPath, `PRAGMA auto_vacuum`),
		"activity has no page-release policy to pay for")
}

func TestStore_CapturesRejectSharedDatabaseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")

	st, err := New(Options{Path: path, CapturesPath: path})
	require.Error(t, err)
	assert.Nil(t, st)
	assert.Contains(t, err.Error(), "captures need their own file")
}

func TestStore_CapturesInMemoryStoreAllowsCapturesPath(t *testing.T) {
	// An in-memory activity store has no file to compare against, so the
	// captures file is accepted; the config layer is what requires store.path.
	st, err := New(Options{
		Path:                 "",
		CapturesPath:         filepath.Join(t.TempDir(), "captures.db"),
		CapturesMaxBytes:     1 << 20,
		CapturesMaxItemBytes: 1 << 20,
	})
	require.NoError(t, err)
	defer st.Close()

	require.NotNil(t, st.Captures())
	assert.True(t, st.IsInMemory())
}

func readInt(t *testing.T, path, query string) int64 {
	t.Helper()

	db := openRaw(t, path)
	var value int64
	require.NoError(t, db.QueryRowContext(context.Background(), query).Scan(&value))

	return value
}

func tableNames(t *testing.T, path string) []string {
	t.Helper()

	db := openRaw(t, path)
	rows, err := db.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	require.NoError(t, err)
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, rows.Err())

	return names
}

func blobOf(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return string(b)
}

// BenchmarkCapturesPutAtCap measures a capture write in steady state at the
// size budget, where every write evicts a capture, releases its pages and
// checkpoints the WAL.
func BenchmarkCapturesPutAtCap(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	st, err := New(Options{
		Path:                 filepath.Join(dir, "activity.db"),
		CapturesPath:         filepath.Join(dir, "captures.db"),
		CapturesMaxBytes:     2_000_000,
		CapturesMaxItemBytes: 10_000,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()

	blob := []byte(blobOf(10_000))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := st.Captures().Put(ctx, i, blob); err != nil {
			b.Fatal(err)
		}
	}
}
