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
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
)

var (
	errCaptureNotFound   = errors.New("capture not found")
	errExceedsCaptureMax = errors.New("capture exceeds maximum store size")
)

const captureFileExt = ".cap"

// captureShardSize groups capture files into decimal folders (id/captureShardSize)
// so a single directory never accumulates an unbounded number of entries. This
// keeps operations efficient on filesystems that degrade with very large
// directories.
const captureShardSize = 1000

// Reconcile loop timing. Package-level vars so tests can shorten the cadence.
var (
	reconcileInterval   = 30 * time.Second
	reconcileFirstDelay = 2 * time.Second
)

// diskCapture is a durable captureStore backed by one file per capture under
// dir/<id/1000>/<id>.cap. Reads and writes probe the exact path directly, so no
// startup scan is needed regardless of how many captures exist: opening is O(1)
// and a lookup is a single filesystem stat. When a byte budget is set, an
// eviction pass runs in the background (never inline on Add, never at open) and
// removes the lowest-id (oldest) captures until the store fits, recomputing the
// total from disk each pass. maxSize == 0 means unlimited (no reconcile loop).
// Activity IDs are monotonic AUTOINCREMENT values, so ascending id order equals
// insertion order. IDs never reach the path from user input.
type diskCapture struct {
	dir     string
	maxSize int64 // 0 = unlimited
	logger  *logmon.Monitor

	dirty     atomic.Bool // set by Add, cleared by the reconcile loop
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

// Add writes a capture atomically (tmp + rename in the target shard dir) and
// flags the store dirty for the background reconcile pass. A single blob larger
// than the whole budget is rejected; it could never fit.
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
	dc.dirty.Store(true)
	return nil
}

// Get reads the capture file for id directly from disk. A missing file reports
// errCaptureNotFound; other I/O errors are surfaced unchanged.
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

// reconcileLoop enforces the byte budget off the request path: after a short
// first delay (so a store with pre-existing files converges without a startup
// scan) it runs on a fixed interval, but skips the disk walk when nothing was
// written since the previous pass.
func (dc *diskCapture) reconcileLoop() {
	defer close(dc.doneCh)

	timer := time.NewTimer(reconcileFirstDelay)
	defer timer.Stop()

	first := true
	for {
		select {
		case <-dc.stopCh:
			return
		case <-timer.C:
		}
		if !first && !dc.dirty.Swap(false) {
			timer.Reset(reconcileInterval)
			continue
		}
		first = false
		if err := dc.reconcile(); err != nil {
			dc.warnf("capture reconcile: %v", err)
		}
		timer.Reset(reconcileInterval)
	}
}

// reconcile sums the sizes of all capture files from disk and, when the total is
// over budget, deletes the lowest-id (oldest) files until it fits. The file list
// is snapshotted first, so captures written concurrently during the pass (always
// higher ids) are never removed here.
func (dc *diskCapture) reconcile() error {
	if dc.maxSize <= 0 {
		return nil
	}

	type entry struct {
		id   int
		path string
		size int64
	}
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
	if total <= dc.maxSize {
		return nil
	}

	slices.SortFunc(entries, func(a, b entry) int { return cmp.Compare(a.id, b.id) })
	for _, e := range entries {
		if total <= dc.maxSize {
			break
		}
		if err := os.Remove(e.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		total -= e.size
	}
	return nil
}

func (dc *diskCapture) warnf(format string, args ...any) {
	if dc.logger != nil {
		dc.logger.Warnf(format, args...)
	}
}
