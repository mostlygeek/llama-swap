package store

import (
	"context"
	"errors"
)

// ErrCaptureTooLarge reports a blob no CaptureRepository can store: it is
// bigger than the repository's per-capture limit. The repository leaves its
// contents and its size accounting unchanged, so callers skip the capture
// instead of evicting stored ones to make room.
var ErrCaptureTooLarge = errors.New("capture exceeds the storage limit")

// CaptureRepository persists opaque request/response capture blobs, keyed by
// the activity row ID each capture belongs to. Callers own the encoding of
// data; the repository treats it as bytes and never inspects it.
//
// The surface is deliberately object-shaped (put/get/delete an object) rather
// than query-shaped: captures are read one at a time by ID and the activity
// table answers "which requests exist". Listing and time-based retention are
// deferred until something consumes them.
//
// A repository enforces its own size budget, the same way the in-memory
// capture cache does: a write that would take the store past its budget evicts
// the oldest captures to make room. The budget is fixed when the repository is
// constructed, so callers never pass a limit per call.
type CaptureRepository interface {
	// Put stores data under id, replacing any capture already stored under
	// it. It returns store.ErrCaptureTooLarge, without storing anything, when
	// data is larger than the repository's per-capture limit. When storing it
	// would take the store past its budget, Put evicts the oldest captures
	// until there is room.
	Put(ctx context.Context, id int, data []byte) error

	// Get returns the bytes stored under id. found is false when no capture
	// is stored under that id.
	Get(ctx context.Context, id int) (data []byte, found bool, err error)

	// Has reports which of ids have a stored capture. Missing IDs are absent
	// from the returned map. It is batched because the activity list marks a
	// whole page of rows in one pass.
	Has(ctx context.Context, ids []int) (map[int]bool, error)

	// Delete removes the capture stored under id. Deleting an id that has no
	// capture is not an error.
	Delete(ctx context.Context, id int) error
}
