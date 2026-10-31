package store

import (
	"context"
	"errors"
	"time"
)

// ErrCaptureExists is returned by CaptureRepository.Put when the ID is
// already taken.
var ErrCaptureExists = errors.New("capture id already exists")

// CaptureEntry is one stored request/response capture. ID is the capture ID
// recorded on the activity row (see NewCaptureID) and Timestamp is when the
// capture was written. Data is opaque to the store: callers own its encoding.
type CaptureEntry struct {
	ID        string
	Timestamp time.Time
	Data      []byte
}

// CaptureRepository persists request/response captures keyed by capture ID.
// It does not depend on the activity repository: an activity row can name a
// capture that was pruned, and a capture can outlive its activity row.
// Captures are bounded by total size rather than by count, since one can be a
// few KB or several MB.
type CaptureRepository interface {
	// Put stores the entry. It returns an error wrapping ErrCaptureExists
	// when a capture with the same ID is already stored, so an ID collision
	// never overwrites an earlier capture. A zero Timestamp is replaced with
	// the current time.
	Put(ctx context.Context, entry CaptureEntry) error

	// Get returns the capture stored under id. found is false when none
	// exists.
	Get(ctx context.Context, id string) (entry CaptureEntry, found bool, err error)

	// Has reports which of ids have a stored capture. IDs without one are
	// absent from the returned map.
	Has(ctx context.Context, ids []string) (map[string]bool, error)

	// Delete removes the capture stored under id. Deleting an id that is not
	// present is not an error.
	Delete(ctx context.Context, id string) error

	// Prune deletes the oldest written captures until the total size of the
	// stored Data is at most maxBytes. A maxBytes of zero or less is a no-op.
	Prune(ctx context.Context, maxBytes int64) error

	// Size returns the total size in bytes of the stored Data.
	Size() int64
}
