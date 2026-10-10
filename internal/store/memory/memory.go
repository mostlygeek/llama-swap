// Package memory implements store.CaptureRepository with captures held in
// this process's memory: a sized FIFO buffer, lost on shutdown.
package memory

import (
	"context"
	"fmt"

	"github.com/mostlygeek/llama-swap/internal/cache"
	"github.com/mostlygeek/llama-swap/internal/store"
)

type captureRepository struct {
	buffer *cache.Cache
	// maxBytes mirrors the buffer's budget, for error messages.
	maxBytes int64
}

var _ store.CaptureRepository = (*captureRepository)(nil)

// New returns a repository holding up to maxBytes of capture blobs, evicting
// the oldest first to make room.
func New(maxBytes int64) store.CaptureRepository {
	return &captureRepository{
		buffer:   cache.New(int(maxBytes)),
		maxBytes: maxBytes,
	}
}

// Put stores data under id, replacing any capture already stored under it,
// and evicts the oldest captures until the new blob fits.
func (r *captureRepository) Put(ctx context.Context, id int, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	if err := r.buffer.Add(id, data); err != nil {
		return fmt.Errorf("%w: capture %d is %d bytes, the whole budget is %d bytes",
			store.ErrCaptureTooLarge, id, len(data), r.maxBytes)
	}
	return nil
}

func (r *captureRepository) Get(ctx context.Context, id int) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("get capture: %w", err)
	}

	data, err := r.buffer.Get(id)
	if err != nil {
		return nil, false, nil
	}
	return data, true, nil
}

// Has reports which of ids hold a capture.
func (r *captureRepository) Has(ctx context.Context, ids []int) (map[int]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("has capture: %w", err)
	}

	found := make(map[int]bool, len(ids))
	for _, id := range ids {
		if r.buffer.Has(id) {
			found[id] = true
		}
	}
	return found, nil
}
