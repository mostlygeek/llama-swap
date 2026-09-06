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

const (
	captureFileExt = ".cap"
	// tmpPrefix is the os.CreateTemp pattern Add uses; tmpLegacyExt was the
	// predictable <id>.cap.tmp name from before the CreateTemp switch. The
	// reconcile walk removes both: with writes serialized under writeMu, any
	// temp file it sees must be a crash leftover, and lingering ones would
	// consume disk the budget silently does not account for.
	tmpPrefix    = ".tmp-"
	tmpLegacyExt = ".cap.tmp"
	// namespacePrefix names each database's capture folder (db-<instanceID>).
	// The prefix keeps namespaces distinct from the numeric shard folders the
	// pre-namespacing layout wrote at the store root.
	namespacePrefix = "db-"
)

// captureShardSize groups files into decimal folders (id/captureShardSize) so
// no single directory grows unbounded on filesystems that degrade with large
// directories.
const captureShardSize = 1000

// diskCapture is a durable captureStore backed by one file per capture under
// dir/db-<dbID>/<id/1000>/<id>.cap: each activity database gets its own folder,
// because ids restart at 1 when the database is swapped or recreated, and a
// folder per id keeps generations from colliding. Reads and writes probe the
// exact path of the current database's namespace, so opening is O(1) and needs
// no startup scan. Eviction runs off the request path: Add signals a background
// loop, which walks the whole store — every generation, so captureMaxMB spans
// them all — and deletes the newest-file folder's lowest-id captures first
// (maxSize 0 = unlimited, no loop). Activity IDs are monotonic AUTOINCREMENT
// values, so id order is insertion order; they never come from user input.
type diskCapture struct {
	dir     string // store root: one folder per database, plus pre-namespacing leftovers
	ns      string // captures of the bound database live here (dir when unbound)
	maxSize int64  // 0 = unlimited
	logger  *logmon.Monitor

	writeMu sync.Mutex // serializes file writes with reconcile walks

	total     atomic.Int64 // bytes on disk; the authoritative reconcile value, kept in step by Add
	triggerCh chan struct{}
	stopCh    chan struct{}
	doneCh    chan struct{}
	closeOnce sync.Once

	onWalkStart func() // test hook: invoked once when a reconcile walk begins
}

// newDiskCapture opens (creating if needed) a disk capture store bound to the
// activity database identified by dbID, and starts its background reconcile
// loop when a budget is set. maxBytes < 0 is a configuration error;
// maxBytes == 0 means unlimited disk usage. An empty dbID skips the identity
// binding (tests); production callers pass store.InstanceID.
func newDiskCapture(dir string, maxBytes int, dbID string, logger *logmon.Monitor) (*diskCapture, error) {
	return newDiskCaptureOpts(dir, maxBytes, dbID, logger, true)
}

// newDiskCaptureOpts is newDiskCapture with control over starting the reconcile
// loop, so tests can drive eviction deterministically via reconcile().
func newDiskCaptureOpts(dir string, maxBytes int, dbID string, logger *logmon.Monitor, startLoop bool) (*diskCapture, error) {
	if maxBytes < 0 {
		return nil, fmt.Errorf("disk capture store: maxBytes must be >= 0, got %d", maxBytes)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("disk capture store: %w", err)
	}
	if info, err := os.Stat(dir); err == nil && sharedWritable(info) && logger != nil {
		logger.Warnf("capture dir %s is group/world-writable (%o): local users can plant or read captures", dir, info.Mode().Perm())
	}
	dc := &diskCapture{dir: dir, ns: dir, maxSize: int64(maxBytes), logger: logger}
	if dbID != "" {
		dc.ns = filepath.Join(dir, namespacePrefix+dbID)
		if err := dc.adoptLegacy(); err != nil {
			return nil, fmt.Errorf("disk capture store: %w", err)
		}
	}
	if startLoop && dc.maxSize > 0 {
		dc.triggerCh = make(chan struct{}, 1)
		dc.stopCh = make(chan struct{})
		dc.doneCh = make(chan struct{})
		go dc.reconcileLoop()
	}
	return dc, nil
}

// adoptLegacy prepares the current database's namespace and moves any
// root-level shard folders into it. That pre-namespacing layout recorded no
// database identity, so the only honest attribution is the database in use
// now; later identity changes are encoded in the folder names themselves and
// need no bookkeeping file.
func (dc *diskCapture) adoptLegacy() error {
	if err := os.MkdirAll(dc.ns, 0o700); err != nil {
		return err
	}
	return dc.migrateRoot(dc.ns)
}

// migrateRoot relocates pre-namespacing shard folders from the store root
// into target in one pass. It runs at construction only, before the reconcile
// loop and request path exist.
func (dc *diskCapture) migrateRoot(target string) error {
	entries, err := os.ReadDir(dc.dir)
	if err != nil {
		return err
	}
	var moved int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, cerr := strconv.Atoi(e.Name()); cerr != nil {
			continue // not a pre-namespacing shard folder
		}
		if moved == 0 {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		}
		from := filepath.Join(dc.dir, e.Name())
		if err := os.Rename(from, filepath.Join(target, e.Name())); err != nil {
			return fmt.Errorf("move legacy capture dir %s: %w", from, err)
		}
		moved++
	}
	if moved > 0 && dc.logger != nil {
		dc.logger.Debugf("moved %d legacy capture folders into %s", moved, target)
	}
	return nil
}

func (dc *diskCapture) shardDir(id int) string {
	return filepath.Join(dc.ns, strconv.Itoa(id/captureShardSize))
}

// captureNamespace returns p's first component under the store root: the
// database folder in the namespaced layout, or a shard folder name for
// pre-namespacing or unbound (dbID-less) layouts.
func captureNamespace(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return ""
	}
	gen, _, _ := strings.Cut(rel, string(filepath.Separator))
	return gen
}

func (dc *diskCapture) path(id int) string {
	return filepath.Join(dc.shardDir(id), strconv.Itoa(id)+captureFileExt)
}

// Add writes a capture atomically (random-named tmp + rename) and signals the
// reconcile loop, serialized against reconcile walks via writeMu. A blob
// larger than the whole budget is rejected; it could never fit.
func (dc *diskCapture) Add(id int, data []byte) error {
	size := int64(len(data))
	if dc.maxSize > 0 && size > dc.maxSize {
		return errExceedsCaptureMax
	}

	dc.writeMu.Lock()
	defer dc.writeMu.Unlock()

	dir := dc.shardDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create capture shard for %d: %w", id, err)
	}
	// A random temp name keeps the path unpredictable, so a planted symlink
	// there cannot redirect the write (O_EXCL also never follows links).
	tmp, err := os.CreateTemp(dir, tmpPrefix+"*")
	if err != nil {
		return fmt.Errorf("create temp capture %d: %w", id, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("write capture %d: %w", id, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("close capture %d: %w", id, err)
	}
	if err := os.Rename(tmp.Name(), dc.path(id)); err != nil {
		os.Remove(tmp.Name())
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
// also corrects drift in the running counter — and deletes the oldest files
// until the store fits: lowest id first within a database folder, folders
// ranked oldest by their newest file, so a replaced database's captures go
// before the current one's. Add is serialized against the walk and eviction
// via writeMu, so the sum used for eviction decisions is exact and a write
// racing the pass can neither over- nor under-evict. The file list is
// snapshotted first so captures written by a later pass (always higher ids)
// are never removed by this one.
func (dc *diskCapture) reconcile() error {
	if dc.maxSize <= 0 {
		return nil
	}

	type entry struct {
		gen  string // first path component under the store root: the database folder
		id   int
		path string
		size int64
	}

	var entries []entry
	var total int64
	genNewest := map[string]int64{} // folder → newest mtime in it
	complete := true                // whether the walk saw the whole store

	dc.writeMu.Lock()
	defer dc.writeMu.Unlock()
	defer func() {
		// A walk that aborted partway holds a partial sum: installing it as
		// the authoritative total would understate usage and mute the trigger
		// predicate, so keep the running counter and retry on the next signal.
		if complete {
			dc.total.Store(total)
		}
	}()
	if dc.onWalkStart != nil {
		dc.onWalkStart()
	}

	err := filepath.WalkDir(dc.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			complete = false
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(p)
		if strings.HasPrefix(base, tmpPrefix) || strings.HasSuffix(base, tmpLegacyExt) {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				complete = false
				return err
			}
			return nil
		}
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
				complete = false
				return serr
			}
		}
		gen := captureNamespace(dc.dir, p)
		if mt := info.ModTime().UnixNano(); mt > genNewest[gen] {
			genNewest[gen] = mt
		}
		entries = append(entries, entry{gen: gen, id: id, path: p, size: info.Size()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return err
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if c := cmp.Compare(genNewest[a.gen], genNewest[b.gen]); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})
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
