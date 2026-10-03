// Package memory implements store.CaptureRepository with captures held in this
// process's memory. It is the behaviour the legacy captureBuffer setting has
// always had — a sized FIFO buffer that is lost on shutdown — expressed in the
// same vocabulary as the persistent backends, so the proxy depends on the
// repository and not on which backend it got.
package memory

import (
	"context"
	"errors"
	"fmt"

	"github.com/mostlygeek/llama-swap/internal/cache"
	"github.com/mostlygeek/llama-swap/internal/store"
)

type captureRepository struct {
	cache        *cache.Cache
	maxBytes     int64
	maxItemBytes int64
}

var _ store.CaptureRepository = (*captureRepository)(nil)

// New returns a repository holding up to maxBytes of capture blobs, evicting
// the oldest first to make room. maxItemBytes rejects a single blob larger
// than that with store.ErrCaptureTooLarge; zero means no per-capture limit.
//
// maxBytes is a budget for this process's heap, not for a file: the persistent
// equivalent is store.captures.maxSizeMB.
func New(maxBytes, maxItemBytes int64) store.CaptureRepository {
	return &captureRepository{
		cache:        cache.New(int(maxBytes)),
		maxBytes:     maxBytes,
		maxItemBytes: maxItemBytes,
	}
}

// Put stores data under id, replacing any capture already stored under it.
// Eviction is FIFO: the cache drops the oldest entries until the new blob fits.
func (r *captureRepository) Put(ctx context.Context, id int, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("put capture: %w", err)
	}

	size := int64(len(data))
	if r.maxItemBytes > 0 && size > r.maxItemBytes {
		return fmt.Errorf("%w: capture %d is %d bytes, limit is %d bytes", store.ErrCaptureTooLarge, id, size, r.maxItemBytes)
	}
	if err := r.cache.Add(id, data); err != nil {
		if errors.Is(err, cache.ErrExceedsMaxSize) {
			return fmt.Errorf("%w: capture %d is %d bytes, the whole budget is %d bytes", store.ErrCaptureTooLarge, id, size, r.maxBytes)
		}
		return fmt.Errorf("put capture: %w", err)
	}
	return nil
}

func (r *captureRepository) Get(ctx context.Context, id int) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("get capture: %w", err)
	}

	data, err := r.cache.Get(id)
	if errors.Is(err, cache.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get capture: %w", err)
	}
	return data, true, nil
}

// Has reports which of ids hold a capture. The cache lookup is a map probe, so
// the batch is a plain loop.
func (r *captureRepository) Has(ctx context.Context, ids []int) (map[int]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("has capture: %w", err)
	}

	found := make(map[int]bool, len(ids))
	for _, id := range ids {
		if r.cache.Has(id) {
			found[id] = true
		}
	}
	return found, nil
}

func (r *captureRepository) Delete(ctx context.Context, id int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete capture: %w", err)
	}

	r.cache.Delete(id)
	return nil
}
