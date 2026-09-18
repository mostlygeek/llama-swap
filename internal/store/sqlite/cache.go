package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mostlygeek/llama-swap/internal/store"
)

// cacheRepository implements store.CacheRepository over the cache table.
//
// Expiry is evaluated in SQL as ts_created + ttl_seconds < now so a read and
// its expiry check are one round trip. Rows with ttl_seconds <= 0 never
// expire.
type cacheRepository struct {
	db *sql.DB
}

var _ store.CacheRepository = (*cacheRepository)(nil)

func (r *cacheRepository) Get(ctx context.Context, key string) (store.CacheEntry, bool, error) {
	var (
		data []byte
		ttl  int64
		ts   int64
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT data, ttl_seconds, ts_created FROM cache WHERE key = ?`,
		key,
	).Scan(&data, &ttl, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return store.CacheEntry{}, false, nil
	}
	if err != nil {
		return store.CacheEntry{}, false, fmt.Errorf("get cache: %w", err)
	}

	entry := store.CacheEntry{
		Key:       key,
		Data:      data,
		TTL:       time.Duration(ttl) * time.Second,
		Timestamp: time.Unix(ts, 0),
	}
	if entry.Expired(time.Now()) {
		// Drop it now so a key that is never written again does not linger
		// until the next Prune.
		if err := r.Delete(ctx, key); err != nil {
			return store.CacheEntry{}, false, err
		}
		return store.CacheEntry{}, false, nil
	}
	return entry, true, nil
}

func (r *cacheRepository) Set(ctx context.Context, entry store.CacheEntry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	ttl := int64(0)
	if entry.TTL > 0 {
		ttl = int64(entry.TTL.Seconds())
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cache (key, data, ttl_seconds, ts_created)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			data = excluded.data,
			ttl_seconds = excluded.ttl_seconds,
			ts_created = excluded.ts_created`,
		entry.Key, entry.Data, ttl, entry.Timestamp.Unix(),
	)
	if err != nil {
		return fmt.Errorf("set cache: %w", err)
	}
	return nil
}

func (r *cacheRepository) Delete(ctx context.Context, key string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM cache WHERE key = ?`, key); err != nil {
		return fmt.Errorf("delete cache: %w", err)
	}
	return nil
}

func (r *cacheRepository) Prune(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM cache
		WHERE ttl_seconds > 0 AND ts_created + ttl_seconds < ?`,
		time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("prune cache: %w", err)
	}
	return nil
}
