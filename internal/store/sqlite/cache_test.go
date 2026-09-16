package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mostlygeek/llama-swap/internal/store"
)

// newCacheStore returns an in-memory store with its cache repository.
func newCacheStore(t *testing.T) (*Store, store.CacheRepository) {
	t.Helper()
	st, err := New("")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return st, st.Cache()
}

func TestStore_CacheSetGet(t *testing.T) {
	ctx := context.Background()
	_, cache := newCacheStore(t)

	// Truncated to the second because the column stores Unix seconds.
	written := time.Now().Truncate(time.Second)
	require.NoError(t, cache.Set(ctx, store.CacheEntry{
		Key:       "capcompat:v1:local:model-a",
		Data:      []byte(`{"context":4096}`),
		TTL:       time.Hour,
		Timestamp: written,
	}))

	got, found, err := cache.Get(ctx, "capcompat:v1:local:model-a")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "capcompat:v1:local:model-a", got.Key)
	assert.Equal(t, []byte(`{"context":4096}`), got.Data)
	assert.Equal(t, time.Hour, got.TTL)
	assert.Equal(t, written.Unix(), got.Timestamp.Unix())
}

func TestStore_CacheGetMissingKey(t *testing.T) {
	ctx := context.Background()
	_, cache := newCacheStore(t)

	_, found, err := cache.Get(ctx, "nope")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestStore_CacheSetDefaultsTimestampToNow(t *testing.T) {
	ctx := context.Background()
	_, cache := newCacheStore(t)

	before := time.Now().Add(-time.Second)
	require.NoError(t, cache.Set(ctx, store.CacheEntry{Key: "k", Data: []byte("v")}))

	got, found, err := cache.Get(ctx, "k")
	require.NoError(t, err)
	require.True(t, found)
	assert.False(t, got.Timestamp.Before(before), "timestamp should default to now")
}

func TestStore_CacheOverwrite(t *testing.T) {
	ctx := context.Background()
	st, cache := newCacheStore(t)

	first := time.Now().Truncate(time.Second).Add(-time.Minute)
	second := first.Add(30 * time.Second)
	require.NoError(t, cache.Set(ctx, store.CacheEntry{
		Key: "k", Data: []byte("first"), TTL: time.Hour, Timestamp: first,
	}))
	require.NoError(t, cache.Set(ctx, store.CacheEntry{
		Key: "k", Data: []byte("second"), TTL: 2 * time.Hour, Timestamp: second,
	}))

	got, found, err := cache.Get(ctx, "k")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []byte("second"), got.Data)
	assert.Equal(t, 2*time.Hour, got.TTL)
	assert.Equal(t, second.Unix(), got.Timestamp.Unix())

	var rows int
	require.NoError(t, st.db.QueryRow(`SELECT COUNT(*) FROM cache`).Scan(&rows))
	assert.Equal(t, 1, rows, "overwrite must not create a second row")
}

func TestStore_CacheExpiredEntryIsGone(t *testing.T) {
	ctx := context.Background()
	st, cache := newCacheStore(t)

	// Written two hours ago with a one hour TTL. No sleeping involved.
	require.NoError(t, cache.Set(ctx, store.CacheEntry{
		Key:       "stale",
		Data:      []byte("old"),
		TTL:       time.Hour,
		Timestamp: time.Now().Add(-2 * time.Hour),
	}))

	_, found, err := cache.Get(ctx, "stale")
	require.NoError(t, err)
	assert.False(t, found)

	// The read deletes the row rather than leaving it for Prune.
	var rows int
	require.NoError(t, st.db.QueryRow(`SELECT COUNT(*) FROM cache`).Scan(&rows))
	assert.Equal(t, 0, rows)
}

func TestStore_CacheZeroTTLNeverExpires(t *testing.T) {
	ctx := context.Background()
	_, cache := newCacheStore(t)

	require.NoError(t, cache.Set(ctx, store.CacheEntry{
		Key:       "forever",
		Data:      []byte("v"),
		Timestamp: time.Now().Add(-10000 * time.Hour),
	}))

	got, found, err := cache.Get(ctx, "forever")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []byte("v"), got.Data)
}

func TestStore_CacheDeleteMissingIsNotAnError(t *testing.T) {
	ctx := context.Background()
	_, cache := newCacheStore(t)

	require.NoError(t, cache.Delete(ctx, "never-existed"))

	require.NoError(t, cache.Set(ctx, store.CacheEntry{Key: "k", Data: []byte("v")}))
	require.NoError(t, cache.Delete(ctx, "k"))
	_, found, err := cache.Get(ctx, "k")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestStore_CachePrune(t *testing.T) {
	ctx := context.Background()
	st, cache := newCacheStore(t)

	now := time.Now()
	require.NoError(t, cache.Set(ctx, store.CacheEntry{
		Key: "expired", Data: []byte("a"), TTL: time.Minute, Timestamp: now.Add(-time.Hour),
	}))
	require.NoError(t, cache.Set(ctx, store.CacheEntry{
		Key: "live", Data: []byte("b"), TTL: time.Hour, Timestamp: now,
	}))
	require.NoError(t, cache.Set(ctx, store.CacheEntry{
		Key: "no-ttl", Data: []byte("c"), Timestamp: now.Add(-10000 * time.Hour),
	}))

	require.NoError(t, cache.Prune(ctx))

	var keys []string
	rows, err := st.db.Query(`SELECT key FROM cache ORDER BY key`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		keys = append(keys, k)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"live", "no-ttl"}, keys)
}

func TestStore_CachePersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.db")

	first, err := New(path)
	require.NoError(t, err)
	require.NoError(t, first.Cache().Set(ctx, store.CacheEntry{
		Key: "k", Data: []byte("persisted"), TTL: time.Hour,
	}))
	require.NoError(t, first.Close())

	second, err := New(path)
	require.NoError(t, err)
	defer second.Close()

	got, found, err := second.Cache().Get(ctx, "k")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []byte("persisted"), got.Data)
}

func TestStore_CachePrunesExpiredRowsOnOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.db")

	first, err := New(path)
	require.NoError(t, err)
	require.NoError(t, first.Cache().Set(ctx, store.CacheEntry{
		Key:       "stale",
		Data:      []byte("old"),
		TTL:       time.Minute,
		Timestamp: time.Now().Add(-time.Hour),
	}))
	require.NoError(t, first.Close())

	second, err := New(path)
	require.NoError(t, err)
	defer second.Close()

	var rows int
	require.NoError(t, second.db.QueryRow(`SELECT COUNT(*) FROM cache`).Scan(&rows))
	assert.Equal(t, 0, rows, "opening the store should prune expired rows")
}

func TestStore_CacheEntryExpired(t *testing.T) {
	base := time.Unix(1750000000, 0)

	tests := []struct {
		name  string
		entry store.CacheEntry
		at    time.Time
		want  bool
	}{
		{"zero ttl never expires", store.CacheEntry{Timestamp: base}, base.Add(1000 * time.Hour), false},
		{"negative ttl never expires", store.CacheEntry{Timestamp: base, TTL: -time.Hour}, base.Add(time.Hour), false},
		{"zero timestamp never expires", store.CacheEntry{TTL: time.Hour}, base, false},
		{"inside ttl", store.CacheEntry{Timestamp: base, TTL: time.Hour}, base.Add(30 * time.Minute), false},
		{"exactly at ttl", store.CacheEntry{Timestamp: base, TTL: time.Hour}, base.Add(time.Hour), false},
		{"past ttl", store.CacheEntry{Timestamp: base, TTL: time.Hour}, base.Add(time.Hour + time.Second), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.entry.Expired(tt.at))
		})
	}
}
