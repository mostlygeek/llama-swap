package store

import (
	"context"
	"errors"
)

// ErrCaptureTooLarge reports a blob bigger than the repository's whole
// budget: nothing is stored and nothing is evicted.
var ErrCaptureTooLarge = errors.New("capture exceeds the storage limit")

// CaptureRepository persists opaque capture blobs, keyed by the activity row
// ID each capture belongs to. It treats data as bytes and never inspects it.
// A repository enforces a size budget fixed at construction: a write that
// would take the store past it evicts the oldest captures to make room.
type CaptureRepository interface {
	// Put stores data under id, replacing any capture already stored under
	// it. It returns store.ErrCaptureTooLarge when data is larger than the
	// repository's whole budget.
	Put(ctx context.Context, id int, data []byte) error

	// Get returns the bytes stored under id. found is false when no capture
	// is stored under that id.
	Get(ctx context.Context, id int) (data []byte, found bool, err error)

	// Has reports which of ids have a stored capture. Missing IDs are absent
	// from the returned map.
	Has(ctx context.Context, ids []int) (map[int]bool, error)
}
