package server

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mostlygeek/llama-swap/internal/logmon"
)

var (
	errCaptureNotFound   = errors.New("capture not found")
	errExceedsCaptureMax = errors.New("capture exceeds maximum store size")
)

const captureFileExt = ".cap"

// captureShardSize groups files into decimal folders (id/captureShardSize) so
// no single directory grows unbounded on filesystems that degrade with large
// directories.
const captureShardSize = 1000

// diskCapture is a durable captureStore backed by one file per capture under
// dir/<id/1000>/<id>.cap. Reads and writes probe the exact path, so opening is
// O(1) and needs no startup scan regardless of capture count. Eviction of the
// lowest-id (oldest) captures runs off the request path: Add signals a
// background loop, which walks the disk only once the running byte total
// crosses maxSize (0 = unlimited, no loop). Activity IDs are monotonic
// AUTOINCREMENT values, so id order is insertion order; they never come from
// user input.
type diskCapture struct {
	dir     string
	maxSize int64 // 0 = unlimited
	logger  *logmon.Monitor

	total     atomic.Int64 // bytes on disk, corrected by each reconcile walk
	triggerCh chan struct{}
	stopCh    chan struct{}
	doneCh    chan struct{}
	closeOnce sync.Once
}

// newDiskCapture opens (creating if needed) a disk capture store and starts its
// background reconcile loop when a budget is set. maxBytes < 0 is a
// configuration error; maxBytes == 0 means unlimited disk usage.
func newDiskCapture(dir string, maxBytes int, logger *logmon.Monitor) (*diskCapture, error) {
	return newDiskCaptureOpts(dir, maxBytes, logger, true)
}

// newDiskCaptureOpts is newDiskCapture with control over starting the reconcile
// loop, so tests can drive eviction deterministically via reconcile().
func newDiskCaptureOpts(dir string, maxBytes int, logger *logmon.Monitor, startLoop bool) (*diskCapture, error) {
	if maxBytes < 0 {
		return nil, fmt.Errorf("disk capture store: maxBytes must be >= 0, got %d", maxBytes)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("disk capture store: %w", err)
	}
	dc := &diskCapture{dir: dir, maxSize: int64(maxBytes), logger: logger}
	if startLoop && dc.maxSize > 0 {
		dc.triggerCh = make(chan struct{}, 1)
		dc.stopCh = make(chan struct{})
		dc.doneCh = make(chan struct{})
		go dc.reconcileLoop()
	}
	return dc, nil
}

func (dc *diskCapture) shardDir(id int) string {
	return filepath.Join(dc.dir, strconv.Itoa(id/captureShardSize))
}

func (dc *diskCapture) path(id int) string {
	return filepath.Join(dc.shardDir(id), strconv.Itoa(id)+captureFileExt)
}

// Add writes a capture atomically (tmp + rename) and signals the reconcile
// loop. A blob larger than the whole budget is rejected; it could never fit.
func (dc *diskCapture) Add(id int, data []byte) error {
	size := int64(len(data))
	if dc.maxSize > 0 && size > dc.maxSize {
		return errExceedsCaptureMax
	}

	if err := os.MkdirAll(dc.shardDir(id), 0o700); err != nil {
		return fmt.Errorf("create capture shard for %d: %w", id, err)
	}
	p := dc.path(id)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write capture %d: %w", id, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("store capture %d: %w", id, err)
	}

	if dc.maxSize > 0 {
		dc.total.Add(size)
		select {
		case dc.triggerCh <- struct{}{}:
		default:
		}
	}
	return nil
}

// Get reads the capture file for id. A missing file reports errCaptureNotFound.
func (dc *diskCapture) Get(id int) ([]byte, error) {
	data, err := os.ReadFile(dc.path(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errCaptureNotFound
		}
		return nil, err
	}
	return data, nil
}

// Has reports whether the capture file exists, by stat-ing its exact path.
func (dc *diskCapture) Has(id int) bool {
	_, err := os.Stat(dc.path(id))
	return err == nil
}

// Close stops the background reconcile loop and waits for it to exit. It is
// safe to call on a store with no loop and is idempotent.
func (dc *diskCapture) Close() error {
	dc.closeOnce.Do(func() {
		if dc.stopCh != nil {
			close(dc.stopCh)
		}
	})
	if dc.doneCh != nil {
		<-dc.doneCh
	}
	return nil
}

// reconcileLoop enforces the byte budget off the request path. It reconciles
// once at startup so over-budget files left by previous runs converge (open
// itself never scans), then whenever Add signals and the running total says
// the store may not fit.
func (dc *diskCapture) reconcileLoop() {
	defer close(dc.doneCh)

	dc.runReconcile()
	for {
		select {
		case <-dc.stopCh:
			return
		case <-dc.triggerCh:
			if dc.total.Load() > dc.maxSize {
				dc.runReconcile()
			}
		}
	}
}

func (dc *diskCapture) runReconcile() {
	if err := dc.reconcile(); err != nil {
		dc.warnf("capture reconcile: %v", err)
	}
}

// reconcile sums all capture files from disk — the authoritative total, which
// also corrects drift in the running counter — and deletes the lowest-id
// (oldest) files until the store fits. The file list is snapshotted first so
// captures written concurrently during the pass (always higher ids) are never
// removed here.
func (dc *diskCapture) reconcile() error {
	if dc.maxSize <= 0 {
		return nil
	}

	type entry struct {
		id   int
		path string
		size int64
	}
	// Apply the walked sum as a delta onto the running counter instead of
	// storing it, so Adds racing the walk are not clobbered. A racing write
	// the walk missed can only skew total high (counted twice); the next
	// walk corrects it.
	before := dc.total.Load()

	var entries []entry
	var total int64

	err := filepath.WalkDir(dc.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		base := filepath.Base(p)
		if !strings.HasSuffix(base, captureFileExt) {
			return nil
		}
		id, cerr := strconv.Atoi(strings.TrimSuffix(base, captureFileExt))
		if cerr != nil {
			return nil
		}
		info, serr := d.Info()
		if serr != nil {
			info, serr = os.Stat(p)
			if serr != nil {
				return nil
			}
		}
		entries = append(entries, entry{id: id, path: p, size: info.Size()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return err
	}
	dc.total.Add(total - before)
	if total <= dc.maxSize {
		return nil
	}

	slices.SortFunc(entries, func(a, b entry) int { return cmp.Compare(a.id, b.id) })
	for _, e := range entries {
		if dc.total.Load() <= dc.maxSize {
			break
		}
		if err := os.Remove(e.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		dc.total.Add(-e.size)
	}
	return nil
}

func (dc *diskCapture) warnf(format string, args ...any) {
	if dc.logger != nil {
		dc.logger.Warnf(format, args...)
	}
}
