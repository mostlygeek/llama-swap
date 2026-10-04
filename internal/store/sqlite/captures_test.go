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
)

// newCaptureStore opens a store whose captures live in their own file.
func newCaptureStore(t *testing.T, maxBytes int64) (*Store, string) {
	t.Helper()

	dir := t.TempDir()
	st, err := New(Options{
		Path:             filepath.Join(dir, "activity.db"),
		CapturesPath:     filepath.Join(dir, "captures.db"),
		CapturesMaxBytes: maxBytes,
	})
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	return st, filepath.Join(dir, "captures.db")
}

// openRaw opens a database file directly, for assertions the API does not
// expose.
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	return db
}

func rowCount(t *testing.T, path string) int {
	t.Helper()

	db := openRaw(t, path)
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM captures`).Scan(&count))

	return count
}

func TestStore_NewCreatesMissingDirectories(t *testing.T) {
	dir := t.TempDir()
	activityPath := filepath.Join(dir, "a", "b", "activity.db")
	capturesPath := filepath.Join(dir, "c", "d", "captures.db")

	st, err := New(Options{
		Path:             activityPath,
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.NoError(t, st.Captures().Put(context.Background(), 1, []byte("x")))
	require.NoError(t, st.Close())

	for _, path := range []string{activityPath, capturesPath} {
		_, err := os.Stat(path)
		require.NoError(t, err, path)
	}
}

func TestStore_CapturesDisabledWithoutPath(t *testing.T) {
	st, err := New(Options{Path: filepath.Join(t.TempDir(), "activity.db")})
	require.NoError(t, err)
	defer st.Close()

	if st.Captures() != nil {
		t.Fatal("Captures() must be nil when no captures file is configured")
	}
}

// The repository contract is asserted for every backend in
// captures_conformance_test.go; the tests below cover the SQLite backend.

func TestStore_CapturesEnforcesLoweredBudgetOnOpen(t *testing.T) {
	const blobSize = 10_000
	ctx := context.Background()

	dir := t.TempDir()
	activityPath := filepath.Join(dir, "activity.db")
	capturesPath := filepath.Join(dir, "captures.db")

	first, err := New(Options{
		Path:             activityPath,
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 400_000,
	})
	require.NoError(t, err)
	for id := 1; id <= 20; id++ {
		require.NoError(t, first.Captures().Put(ctx, id, []byte(blobOf(blobSize))))
	}
	require.Equal(t, 20, rowCount(t, capturesPath))
	require.NoError(t, first.Close())

	// 200 KB stored under a 150 KB budget evicts the oldest 50 KB.
	second, err := New(Options{
		Path:             activityPath,
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 150_000,
	})
	require.NoError(t, err)
	defer second.Close()

	count := rowCount(t, capturesPath)
	require.Greater(t, count, 5)
	require.Less(t, count, 20)
	assert.Equal(t, 15, count, "the budget should free exactly the excess")

	found, err := second.Captures().Has(ctx, []int{1, 20})
	require.NoError(t, err)
	assert.NotContains(t, found, 1, "the oldest capture should be evicted")
	assert.Contains(t, found, 20, "the newest capture should survive")
}

func TestStore_CapturesPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	opts := Options{
		Path:             filepath.Join(dir, "activity.db"),
		CapturesPath:     filepath.Join(dir, "captures.db"),
		CapturesMaxBytes: 1 << 20,
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
		Path:             activityPath,
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.NoError(t, st.Captures().Put(ctx, 1, []byte("x")))
	require.NoError(t, st.Close())

	capturesTables := tableNames(t, capturesPath)
	assert.Contains(t, capturesTables, "captures")
	assert.NotContains(t, capturesTables, "activity")
	assert.NotContains(t, capturesTables, "cache")

	activityTables := tableNames(t, activityPath)
	assert.Contains(t, activityTables, "activity")
	assert.NotContains(t, activityTables, "captures")
}

func TestStore_CapturesRejectSharedDatabaseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")

	st, err := New(Options{Path: path, CapturesPath: path})
	require.Error(t, err)
	assert.Nil(t, st)
	assert.Contains(t, err.Error(), "captures need their own file")
}

func TestStore_CapturesInMemoryStoreAllowsCapturesPath(t *testing.T) {
	// Only the config layer requires store.path.
	st, err := New(Options{
		Path:             "",
		CapturesPath:     filepath.Join(t.TempDir(), "captures.db"),
		CapturesMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	defer st.Close()

	require.NotNil(t, st.Captures())
	assert.True(t, st.IsInMemory())
}

// databaseIDIn reads a file's meta.database_id directly.
func databaseIDIn(t *testing.T, path string) string {
	t.Helper()

	db := openRaw(t, path)
	var id string
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT value FROM meta WHERE key = 'database_id'`).Scan(&id))

	return id
}

// The activity and captures files must record the same identity.
func TestStore_DatabasesShareIdentity(t *testing.T) {
	dir := t.TempDir()
	activityPath := filepath.Join(dir, "activity.db")
	capturesPath := filepath.Join(dir, "captures.db")

	st, err := New(Options{
		Path:             activityPath,
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.NoError(t, st.Close())

	activityID := databaseIDIn(t, activityPath)
	capturesID := databaseIDIn(t, capturesPath)
	assert.NotEmpty(t, activityID)
	assert.Equal(t, activityID, capturesID)
}

// A captures file from a different activity database must be moved aside and
// replaced by a fresh one.
func TestStore_MismatchedCapturesDatabaseRotatedAside(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	capturesPath := filepath.Join(dir, "captures.db")
	activityPath := filepath.Join(dir, "activity.db")

	first, err := New(Options{
		Path:             activityPath,
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.NoError(t, first.Captures().Put(ctx, 1, []byte("old")))
	require.NoError(t, first.Close())

	// A brand-new activity database paired with the stale captures file.
	recreatedActivityPath := filepath.Join(dir, "activity-recreated.db")
	second, err := New(Options{
		Path:             recreatedActivityPath,
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	defer second.Close()

	_, err = os.Stat(capturesPath + ".1")
	require.NoError(t, err, "the mismatched captures file should move to captures.db.1")

	_, found, err := second.Captures().Get(ctx, 1)
	require.NoError(t, err)
	assert.False(t, found, "a fresh captures database must not expose the old capture")
	assert.Equal(t, databaseIDIn(t, recreatedActivityPath), databaseIDIn(t, capturesPath))
}

// A deleted captures file is a missing file, not a mismatched one: it is
// recreated in place and adopts the activity database's identity.
func TestStore_DeletedCapturesDatabaseIsRecreated(t *testing.T) {
	dir := t.TempDir()
	activityPath := filepath.Join(dir, "activity.db")
	capturesPath := filepath.Join(dir, "captures.db")

	first, err := New(Options{
		Path:             activityPath,
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.NoError(t, first.Close())

	require.NoError(t, os.Remove(capturesPath))

	second, err := New(Options{
		Path:             activityPath,
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	defer second.Close()

	if _, err := os.Stat(capturesPath + ".1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a deleted captures database must be recreated in place, not moved aside")
	}
	assert.Equal(t, databaseIDIn(t, activityPath), databaseIDIn(t, capturesPath))
}

// Rotation must not overwrite an existing aside file.
func TestStore_RotationUsesLowestFreeSuffix(t *testing.T) {
	dir := t.TempDir()
	capturesPath := filepath.Join(dir, "captures.db")

	first, err := New(Options{
		Path:             filepath.Join(dir, "activity-a.db"),
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.NoError(t, first.Close())

	require.NoError(t, os.WriteFile(capturesPath+".1", []byte("occupied"), 0o600))

	second, err := New(Options{
		Path:             filepath.Join(dir, "activity-b.db"),
		CapturesPath:     capturesPath,
		CapturesMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	defer second.Close()

	_, err = os.Stat(capturesPath + ".2")
	require.NoError(t, err, "rotation must skip an existing captures.db.1")
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

// BenchmarkCapturesPutAtCap measures a capture write at the size budget,
// where every write evicts the oldest capture.
func BenchmarkCapturesPutAtCap(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	st, err := New(Options{
		Path:             filepath.Join(dir, "activity.db"),
		CapturesPath:     filepath.Join(dir, "captures.db"),
		CapturesMaxBytes: 2_000_000,
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
