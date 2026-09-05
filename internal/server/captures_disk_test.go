package server

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/cache"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/store"
)

// newTestDiskCapture opens a disk store with the background reconcile loop
// disabled so tests drive eviction deterministically via reconcile().
func newTestDiskCapture(t *testing.T, maxBytes int) *diskCapture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "captures")
	dc, err := newDiskCaptureOpts(dir, maxBytes, nil, false)
	if err != nil {
		t.Fatalf("newDiskCaptureOpts: %v", err)
	}
	t.Cleanup(func() { dc.Close() })
	return dc
}

func TestCaptureDisk_Roundtrip(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)
	if err := dc.Add(5, []byte("hello")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !dc.Has(5) {
		t.Fatal("Has(5) = false, want true")
	}
	data, err := dc.Get(5)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("Get = %q, want %q", data, "hello")
	}
}

func TestCaptureDisk_GetMissing(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)
	if dc.Has(42) {
		t.Fatal("Has(42) on empty store = true")
	}
	_, err := dc.Get(42)
	if !errors.Is(err, errCaptureNotFound) {
		t.Fatalf("Get missing err = %v, want errCaptureNotFound", err)
	}
}

func TestCaptureDisk_TooLargeRejected(t *testing.T) {
	dc := newTestDiskCapture(t, 16)
	if err := dc.Add(1, make([]byte, 17)); !errors.Is(err, errExceedsCaptureMax) {
		t.Fatalf("Add oversized err = %v, want errExceedsCaptureMax", err)
	}
	if dc.Has(1) {
		t.Fatal("oversized capture was stored")
	}
}

// TestCaptureDisk_UnlimitedWhenMaxIsZero shows captureMaxMB == 0 means no disk
// budget: nothing is rejected for size and reconcile never evicts.
func TestCaptureDisk_UnlimitedWhenMaxIsZero(t *testing.T) {
	dc := newTestDiskCapture(t, 0)
	for id := 1; id <= 50; id++ {
		if err := dc.Add(id, make([]byte, 1000)); err != nil {
			t.Fatalf("Add %d under unlimited budget: %v", id, err)
		}
	}
	if err := dc.reconcile(); err != nil {
		t.Fatalf("reconcile under unlimited budget: %v", err)
	}
	for id := 1; id <= 50; id++ {
		if !dc.Has(id) {
			t.Fatalf("id %d evicted despite unlimited budget", id)
		}
	}
}

// TestCaptureDisk_ReconcileEvictsOldest proves eviction is reconcile-driven, not
// inline: Add past the budget keeps everything until a reconcile pass runs, then
// the lowest (oldest) ids go first until the total fits.
func TestCaptureDisk_ReconcileEvictsOldest(t *testing.T) {
	dc := newTestDiskCapture(t, 20)
	for id := 1; id <= 3; id++ {
		if err := dc.Add(id, make([]byte, 10)); err != nil {
			t.Fatalf("Add %d: %v", id, err)
		}
	}
	if !dc.Has(1) || !dc.Has(2) || !dc.Has(3) {
		t.Fatal("Add must not evict inline; only reconcile enforces the budget")
	}
	if err := dc.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if dc.Has(1) {
		t.Fatal("oldest (id 1) should be evicted")
	}
	if !dc.Has(2) || !dc.Has(3) {
		t.Fatal("newest captures should be retained")
	}
}

func TestCaptureDisk_OverwriteSameID(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)
	if err := dc.Add(7, []byte("one")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := dc.Add(7, []byte("two-and-longer")); err != nil {
		t.Fatalf("Add overwrite: %v", err)
	}
	data, err := dc.Get(7)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(data) != "two-and-longer" {
		t.Fatalf("Get = %q, want overwritten value", data)
	}
}

// TestCaptureDisk_ShardsByThousandFiles checks the on-disk layout: ids are grouped
// into decimal folders of 1000 (id/1000), no padding, and no flat files remain.
func TestCaptureDisk_ShardsByThousandFiles(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)
	for _, id := range []int{7, 999, 1000, 1050, 42_501} {
		if err := dc.Add(id, []byte("x")); err != nil {
			t.Fatalf("Add %d: %v", id, err)
		}
	}
	cases := map[int]string{
		7:      "0/7.cap",
		999:    "0/999.cap",
		1000:   "1/1000.cap",
		1050:   "1/1050.cap",
		42_501: "42/42501.cap",
	}
	for id, rel := range cases {
		want := filepath.Join(dc.dir, filepath.FromSlash(rel))
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("id %d not at %s: %v", id, rel, err)
		}
		// path() and direct layout must agree.
		if got := dc.path(id); got != want {
			t.Fatalf("path(%d) = %s, want %s", id, got, want)
		}
	}
	// Nothing lives at the flat top level.
	entries, err := os.ReadDir(dc.dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("unexpected top-level file %s (expected shard directories)", e.Name())
		}
	}
}

// TestCaptureDisk_LazyDiscoveryAcrossReopen proves no boot scan is required: a
// fresh store with an empty in-memory state finds files purely by probing the
// exact sharded path.
func TestCaptureDisk_LazyDiscoveryAcrossReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	dc, err := newDiskCaptureOpts(dir, 1<<20, nil, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := dc.Add(1050, []byte("persisted")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := dc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	dc2, err := newDiskCaptureOpts(dir, 1<<20, nil, false)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer dc2.Close()
	if !dc2.Has(1050) {
		t.Fatal("capture lost after reopen (lazy discovery failed)")
	}
	data, err := dc2.Get(1050)
	if err != nil || string(data) != "persisted" {
		t.Fatalf("Get after reopen = %q, %v", data, err)
	}
}

// TestCaptureDisk_NoEvictionAtOpen proves opening never scans or evicts: over-budget
// files from a previous run all survive until reconcile runs. This is the startup
// cost guarantee (O(1) open regardless of capture count).
func TestCaptureDisk_NoEvictionAtOpen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	dc, err := newDiskCaptureOpts(dir, 30, nil, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for id := 1; id <= 5; id++ {
		if err := dc.Add(id, make([]byte, 10)); err != nil { // 50 bytes over a 30 budget
			t.Fatalf("Add %d: %v", id, err)
		}
	}
	dc.Close()

	dc2, err := newDiskCaptureOpts(dir, 30, nil, false)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer dc2.Close()
	for id := 1; id <= 5; id++ {
		if !dc2.Has(id) {
			t.Fatalf("id %d evicted at open: reopening must not scan or evict", id)
		}
	}
	// One reconcile pass now enforces the budget by dropping the oldest ids.
	if err := dc2.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if dc2.Has(1) || dc2.Has(2) {
		t.Fatal("reconcile must evict oldest ids to fit the budget")
	}
	for id := 3; id <= 5; id++ {
		if !dc2.Has(id) {
			t.Fatalf("id %d should be retained after reconcile", id)
		}
	}
}

func TestCaptureDisk_MissingFileTreatedAsNotFound(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)
	if err := dc.Add(9, []byte("x")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := os.Remove(dc.path(9)); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if dc.Has(9) {
		t.Fatal("Has must be false after the file is removed")
	}
	if _, err := dc.Get(9); !errors.Is(err, errCaptureNotFound) {
		t.Fatalf("Get missing file err = %v, want errCaptureNotFound", err)
	}
}

// TestCaptureDisk_ReconcileLoopEvictsInBackground exercises the goroutine that the
// production constructor starts: after writes push the store over budget, the
// background pass evicts the oldest ids without an explicit reconcile call.
func TestCaptureDisk_ReconcileLoopEvictsInBackground(t *testing.T) {
	prevInterval, prevFirst := reconcileInterval, reconcileFirstDelay
	reconcileFirstDelay, reconcileInterval = 5*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { reconcileInterval, reconcileFirstDelay = prevInterval, prevFirst })

	dir := filepath.Join(t.TempDir(), "captures")
	dc, err := newDiskCapture(dir, 20, nil) // startLoop = true
	if err != nil {
		t.Fatalf("newDiskCapture: %v", err)
	}
	defer dc.Close()

	for id := 1; id <= 3; id++ { // 30 bytes over a 20 budget
		if err := dc.Add(id, make([]byte, 10)); err != nil {
			t.Fatalf("Add %d: %v", id, err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !dc.Has(1) {
			return // background reconcile evicted the oldest: pass
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("background reconcile did not evict the oldest capture")
}

// TestCaptureDisk_TieredFallsBackToDisk proves a two-tier store writes to both
// tiers, serves reads from memory when present, and falls back to disk once the
// (small) memory tier has evicted the entry.
func TestCaptureDisk_TieredFallsBackToDisk(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)
	mem := cache.New(16)
	layered := combineCapture(mem, dc)

	if err := layered.Add(1, []byte("payload")); err != nil {
		t.Fatalf("tiered Add: %v", err)
	}
	if !mem.Has(1) || !dc.Has(1) {
		t.Fatal("tiered Add must write to both tiers")
	}
	mem.Clear()
	if !layered.Has(1) {
		t.Fatal("Has must report disk tier after memory eviction")
	}
	data, err := layered.Get(1)
	if err != nil || string(data) != "payload" {
		t.Fatalf("fallback Get = %q, %v", data, err)
	}
}

// TestCaptureDisk_TieredMemTooLargeStoredOnDisk verifies an item too big for the
// memory tier is still durably stored on disk and reports as present.
func TestCaptureDisk_TieredMemTooLargeStoredOnDisk(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)
	mem := cache.New(16)
	layered := combineCapture(mem, dc)

	big := make([]byte, 4096)
	if err := layered.Add(2, big); err != nil {
		t.Fatalf("tiered Add oversized-for-mem: %v", err)
	}
	if mem.Has(2) {
		t.Fatal("oversized item unexpectedly in memory tier")
	}
	if !dc.Has(2) {
		t.Fatal("oversized item must be retained on disk")
	}
}

// TestCaptureDisk_CloseStopsLoop proves Close terminates the background reconcile
// goroutine (waited on, not just signaled), so reloads do not leak reconcilers.
func TestCaptureDisk_CloseStopsLoop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	dc, err := newDiskCapture(dir, 1<<20, nil) // loop started
	if err != nil {
		t.Fatalf("newDiskCapture: %v", err)
	}
	done := make(chan struct{})
	go func() { dc.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not stop the reconcile loop")
	}
	// Close must be idempotent.
	if err := dc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// newPersistMetricsMonitor builds a metricsMonitor backed by a persistent sqlite
// store and a disk capture dir, exercising the real two-tier constructor. It
// registers mm.Close so the background reconcile goroutine is stopped at cleanup.
func newPersistMetricsMonitor(t *testing.T, dir string, memMB, diskMB int) *metricsMonitor {
	t.Helper()
	st, err := store.New(filepath.Join(dir, "activity.sqlite"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	mm := newMetricsMonitorWithDisk(logmon.NewWriter(io.Discard), 0, memMB, diskMB, filepath.Join(dir, "captures"), st)
	t.Cleanup(func() { mm.Close() })
	return mm
}

// TestCaptureDisk_MonitorPersistsAcrossRestart shows a capture written through
// the metricsMonitor is retrievable by a fresh monitor over the same dir via the
// lazy disk path, i.e. it survives a process restart (which the in-memory-only
// path cannot) without any boot-time scan.
func TestCaptureDisk_MonitorPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	mm := newPersistMetricsMonitor(t, dir, 0, 8)
	if !mm.enableCaptures {
		t.Fatal("captures should be enabled when a disk tier is configured")
	}
	if !mm.addCapture(ReqRespCapture{ID: 1050, ReqBody: []byte("durable")}) {
		t.Fatal("addCapture returned false")
	}

	reopened := newPersistMetricsMonitor(t, dir, 0, 8)
	got := reopened.getCaptureByID(1050)
	if got == nil || string(got.ReqBody) != "durable" {
		t.Fatalf("capture lost after restart: %+v", got)
	}

	entries := []ActivityLogEntry{{ID: 1050}}
	reopened.overlayCaptureState(entries)
	if !entries[0].HasCapture {
		t.Fatal("overlayCaptureState must report the persisted capture")
	}
}

// TestCaptureDisk_MonitorDisabledWithoutPersistentStore verifies the gate: a
// configured disk tier is refused when the activity DB is in-memory, since IDs
// reset every boot and would orphan capture files.
func TestCaptureDisk_MonitorDisabledWithoutPersistentStore(t *testing.T) {
	st, err := store.New("")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()
	mm := newMetricsMonitorWithDisk(logmon.NewWriter(io.Discard), 0, 0, 8, filepath.Join(t.TempDir(), "caps"), st)
	if mm.enableCaptures {
		t.Fatal("disk captures must be disabled with an in-memory store")
	}
	if mm.captureCache != nil {
		t.Fatal("no capture tier should be built")
	}
}

// TestCaptureDisk_MonitorDisabledWithoutDir verifies the enable switch is the
// directory, not the byte budget: an empty captureDir disables the disk tier
// even when captureMaxMB is positive.
func TestCaptureDisk_MonitorDisabledWithoutDir(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "activity.sqlite"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()
	mm := newMetricsMonitorWithDisk(logmon.NewWriter(io.Discard), 0, 0, 512, "", st)
	if mm.enableCaptures {
		t.Fatal("disk captures must be disabled when captureDir is empty")
	}
	if mm.captureCache != nil {
		t.Fatal("no capture tier should be built without a captureDir")
	}
}
