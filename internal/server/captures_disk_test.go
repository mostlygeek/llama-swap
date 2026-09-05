package server

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/cache"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/store"
)

func newTestDiskCapture(t *testing.T, maxBytes int) *diskCapture {
	t.Helper()
	dir := t.TempDir()
	dc, err := newDiskCapture(filepath.Join(dir, "captures"), maxBytes)
	if err != nil {
		t.Fatalf("newDiskCapture: %v", err)
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
// budget: nothing is rejected for size and nothing is evicted.
func TestCaptureDisk_UnlimitedWhenMaxIsZero(t *testing.T) {
	dc := newTestDiskCapture(t, 0)
	for id := 1; id <= 50; id++ {
		if err := dc.Add(id, make([]byte, 1000)); err != nil {
			t.Fatalf("Add %d under unlimited budget: %v", id, err)
		}
	}
	for id := 1; id <= 50; id++ {
		if !dc.Has(id) {
			t.Fatalf("id %d evicted despite unlimited budget", id)
		}
	}
}

func TestCaptureDisk_EvictsOldestFirst(t *testing.T) {
	// budget fits two 10-byte captures; the third evicts id 1 (lowest id).
	dc := newTestDiskCapture(t, 20)
	for id := 1; id <= 3; id++ {
		if err := dc.Add(id, make([]byte, 10)); err != nil {
			t.Fatalf("Add %d: %v", id, err)
		}
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

func TestCaptureDisk_SurvivesReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	dc, err := newDiskCapture(dir, 1<<20)
	if err != nil {
		t.Fatalf("newDiskCapture: %v", err)
	}
	if err := dc.Add(11, []byte("persisted")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := dc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	dc2, err := newDiskCapture(dir, 1<<20)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer dc2.Close()
	if !dc2.Has(11) {
		t.Fatal("capture lost after reopen")
	}
	data, err := dc2.Get(11)
	if err != nil || string(data) != "persisted" {
		t.Fatalf("Get after reopen = %q, %v", data, err)
	}
}

func TestCaptureDisk_RebuiltIndexHonorsBudget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	dc, _ := newDiskCapture(dir, 20)
	for id := 1; id <= 2; id++ {
		if err := dc.Add(id, make([]byte, 10)); err != nil {
			t.Fatalf("Add %d: %v", id, err)
		}
	}
	dc.Close()

	// Reopen with the same budget: total is rebuilt from disk, so adding a
	// third capture must evict the oldest (id 1), not exceed the budget.
	dc2, err := newDiskCapture(dir, 20)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer dc2.Close()
	if err := dc2.Add(3, make([]byte, 10)); err != nil {
		t.Fatalf("Add after reopen: %v", err)
	}
	if dc2.Has(1) {
		t.Fatal("rebuilt index did not evict oldest id")
	}
	if !dc2.Has(3) {
		t.Fatal("new capture missing")
	}
}

func TestCaptureDisk_MissingFileTreatedAsNotFound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	dc, _ := newDiskCapture(dir, 1<<20)
	if err := dc.Add(9, []byte("x")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := os.Remove(dc.path(9)); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := dc.Get(9); !errors.Is(err, errCaptureNotFound) {
		t.Fatalf("Get missing file err = %v, want errCaptureNotFound", err)
	}
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

// newPersistMetricsMonitor builds a metricsMonitor backed by a persistent sqlite
// store and a disk capture dir, exercising the real two-tier constructor.
func newPersistMetricsMonitor(t *testing.T, dir string, memMB, diskMB int) *metricsMonitor {
	t.Helper()
	st, err := store.New(filepath.Join(dir, "activity.sqlite"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	mm := newMetricsMonitorWithDisk(logmon.NewWriter(io.Discard), 0, memMB, diskMB, filepath.Join(dir, "captures"), st)
	return mm
}

// TestCaptureDisk_MonitorPersistsAcrossRestart shows a capture written through
// the metricsMonitor is retrievable by a fresh monitor over the same dir, i.e.
// it survives a process restart (which the in-memory-only path cannot).
func TestCaptureDisk_MonitorPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	mm := newPersistMetricsMonitor(t, dir, 0, 8)
	if !mm.enableCaptures {
		t.Fatal("captures should be enabled when a disk tier is configured")
	}
	if !mm.addCapture(ReqRespCapture{ID: 4, ReqBody: []byte("durable")}) {
		t.Fatal("addCapture returned false")
	}

	reopened := newPersistMetricsMonitor(t, dir, 0, 8)
	got := reopened.getCaptureByID(4)
	if got == nil || string(got.ReqBody) != "durable" {
		t.Fatalf("capture lost after restart: %+v", got)
	}

	entries := []ActivityLogEntry{{ID: 4}}
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
