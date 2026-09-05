package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

var (
	errCaptureNotFound   = errors.New("capture not found")
	errExceedsCaptureMax = errors.New("capture exceeds maximum store size")
)

const captureFileExt = ".cap"

// diskCapture is a durable captureStore backed by one file per capture under
// dir, plus an in-memory index (id -> size) so Has is O(1) and the FIFO byte
// budget is enforced across restarts. maxSize == 0 means unlimited (no size
// cap, no eviction). Activity IDs are monotonic AUTOINCREMENT values, so
// ascending id order equals insertion order and is a valid FIFO eviction order.
// Filenames are "<id>.cap"; IDs never reach the path from user input.
type diskCapture struct {
	dir     string
	maxSize int64 // 0 = unlimited

	mu    sync.Mutex
	items map[int]int64
	order []int
	total int64
}

// newDiskCapture opens (creating if needed) a disk capture store. maxBytes < 0
// is a configuration error; maxBytes == 0 means unlimited disk usage.
func newDiskCapture(dir string, maxBytes int) (*diskCapture, error) {
	if maxBytes < 0 {
		return nil, fmt.Errorf("disk capture store: maxBytes must be >= 0, got %d", maxBytes)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("disk capture store: %w", err)
	}
	dc := &diskCapture{dir: dir, maxSize: int64(maxBytes), items: make(map[int]int64)}
	if err := dc.loadIndex(); err != nil {
		return nil, fmt.Errorf("disk capture store: load index: %w", err)
	}
	return dc, nil
}

// loadIndex rebuilds the in-memory index from files already on disk so the
// byte budget stays correct across restarts.
func (dc *diskCapture) loadIndex() error {
	var ids []int
	err := filepath.WalkDir(dc.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasSuffix(base, captureFileExt) {
			return nil
		}
		id, err := strconv.Atoi(strings.TrimSuffix(base, captureFileExt))
		if err != nil {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil
		}
		dc.items[id] = info.Size()
		ids = append(ids, id)
		return nil
	})
	if err != nil {
		return err
	}
	slices.Sort(ids)
	for _, id := range ids {
		dc.order = append(dc.order, id)
		dc.total += dc.items[id]
	}
	return nil
}

func (dc *diskCapture) path(id int) string {
	return filepath.Join(dc.dir, strconv.Itoa(id)+captureFileExt)
}

func (dc *diskCapture) Add(id int, data []byte) error {
	size := int64(len(data))
	if dc.maxSize > 0 && size > dc.maxSize {
		return errExceedsCaptureMax
	}

	dc.mu.Lock()
	defer dc.mu.Unlock()

	if old, exists := dc.items[id]; exists {
		dc.total -= old
		dc.removeOrder(id)
		os.Remove(dc.path(id))
	}

	if dc.maxSize > 0 {
		for dc.total+size > dc.maxSize && len(dc.order) > 0 {
			dc.evictOldest()
		}
	}

	tmp := dc.path(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write capture %d: %w", id, err)
	}
	if err := os.Rename(tmp, dc.path(id)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("store capture %d: %w", id, err)
	}
	dc.items[id] = size
	dc.order = append(dc.order, id)
	dc.total += size
	return nil
}

func (dc *diskCapture) Get(id int) ([]byte, error) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if _, exists := dc.items[id]; !exists {
		return nil, errCaptureNotFound
	}
	data, err := os.ReadFile(dc.path(id))
	if err != nil {
		// The index claims the file but it is gone (external delete); drop the
		// stale entry so Has and future budgets stay truthful.
		dc.total -= dc.items[id]
		delete(dc.items, id)
		dc.removeOrder(id)
		return nil, errCaptureNotFound
	}
	return data, nil
}

func (dc *diskCapture) Has(id int) bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	_, exists := dc.items[id]
	return exists
}

func (dc *diskCapture) Close() error { return nil }

// evictOldest removes the lowest-id (oldest) capture. Caller holds dc.mu.
func (dc *diskCapture) evictOldest() {
	oldest := dc.order[0]
	dc.order = dc.order[1:]
	if size, exists := dc.items[oldest]; exists {
		dc.total -= size
		delete(dc.items, oldest)
		os.Remove(dc.path(oldest))
	}
}

func (dc *diskCapture) removeOrder(id int) {
	i := slices.Index(dc.order, id)
	if i >= 0 {
		dc.order = slices.Delete(dc.order, i, i+1)
	}
}
