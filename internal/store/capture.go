package store

import (
	"context"
	"time"
)

// CaptureEntry is one stored request/response capture. It belongs to the
// activity row with the same ID, and Timestamp is that row's Timestamp; the
// pair is how a capture database is checked against its activity database.
// Data is opaque to the store: callers own its encoding.
type CaptureEntry struct {
	ID        int
	Timestamp time.Time
	Data      []byte
}

// CaptureRepository persists request/response captures keyed by activity ID.
// Captures are bounded by total size rather than by count, since one can be a
// few KB or several MB.
type CaptureRepository interface {
	// Put stores the entry, replacing any capture with the same ID. A zero
	// Timestamp is replaced with the current time.
	Put(ctx context.Context, entry CaptureEntry) error

	// Get returns the capture stored under id. found is false when none
	// exists.
	Get(ctx context.Context, id int) (entry CaptureEntry, found bool, err error)

	// Has reports which of ids have a stored capture. IDs without one are
	// absent from the returned map.
	Has(ctx context.Context, ids []int) (map[int]bool, error)

	// Delete removes the capture stored under id. Deleting an id that is not
	// present is not an error.
	Delete(ctx context.Context, id int) error

	// Prune deletes the oldest captures until the total size of the stored
	// Data is at most maxBytes. A maxBytes of zero or less is a no-op.
	Prune(ctx context.Context, maxBytes int64) error

	// Size returns the total size in bytes of the stored Data.
	Size() int64
}
