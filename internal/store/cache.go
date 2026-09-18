package store

import (
	"context"
	"time"
)

// CacheEntry is one cached blob. It is a general purpose key/value record:
// callers own the key namespace and the encoding of Data.
//
// TTL bounds how long the entry stays readable. A zero or negative TTL never
// expires, which suits data that is rewritten on a known event rather than
// aged out. Timestamp is when the entry was written; a zero Timestamp is
// replaced with the current time on Set.
type CacheEntry struct {
	Key       string
	Data      []byte
	TTL       time.Duration
	Timestamp time.Time
}

// Expired reports whether the entry is past its TTL at time at. Entries with
// a zero or negative TTL, or a zero Timestamp, never expire.
func (e CacheEntry) Expired(at time.Time) bool {
	if e.TTL <= 0 || e.Timestamp.IsZero() {
		return false
	}
	return at.After(e.Timestamp.Add(e.TTL))
}

// CacheRepository persists opaque blobs under caller-chosen keys. It is a
// cache, not a source of truth: an entry can disappear at any time, whether
// because it expired, because it was pruned, or because the backing store is
// in-memory and the process restarted.
type CacheRepository interface {
	// Get returns the entry stored under key. found is false when no entry
	// exists or the entry has expired; an expired entry is deleted as a side
	// effect of the read.
	Get(ctx context.Context, key string) (entry CacheEntry, found bool, err error)

	// Set inserts the entry, replacing any entry already stored under the
	// same key. A zero Timestamp is replaced with the current time.
	Set(ctx context.Context, entry CacheEntry) error

	// Delete removes the entry stored under key. Deleting a key that is not
	// present is not an error.
	Delete(ctx context.Context, key string) error

	// Prune deletes every expired entry. Entries with no TTL are kept.
	Prune(ctx context.Context) error
}
