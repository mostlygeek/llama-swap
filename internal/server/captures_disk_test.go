package server

import (
	"bytes"
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
	dc, err := newDiskCaptureOpts(dir, maxBytes, "db", nil, false)
	if err != nil {
		t.Fatalf("newDiskCaptureOpts: %v", err)
	}
	t.Cleanup(func() { dc.Close() })
	return dc
}

// TestCaptureDisk_RoundtripAndMissing covers store/lookup of a capture and the
// not-found contract: never-stored and externally-deleted ids both report
// errCaptureNotFound.
func TestCaptureDisk_RoundtripAndMissing(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)

	if dc.Has(42) {
		t.Fatal("Has(42) on empty store = true")
	}
	if _, err := dc.Get(42); !errors.Is(err, errCaptureNotFound) {
		t.Fatalf("Get missing err = %v, want errCaptureNotFound", err)
	}

	if err := dc.Add(5, []byte("hello")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !dc.Has(5) {
		t.Fatal("Has(5) = false, want true")
	}
	if data, err := dc.Get(5); err != nil || string(data) != "hello" {
		t.Fatalf("Get = %q, %v, want %q", data, err, "hello")
	}

	if err := os.Remove(dc.path(5)); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if dc.Has(5) {
		t.Fatal("Has must be false after the file is removed")
	}
	if _, err := dc.Get(5); !errors.Is(err, errCaptureNotFound) {
		t.Fatalf("Get removed err = %v, want errCaptureNotFound", err)
	}
}

// TestCaptureDisk_BudgetEnforcement covers the budget rules: max == 0 stores
// everything and never evicts; a blob over the whole budget is rejected
// upfront; eviction is reconcile-driven (Add never evicts inline) and the
// oldest ids go first until the total fits.
func TestCaptureDisk_BudgetEnforcement(t *testing.T) {
	unlimited := newTestDiskCapture(t, 0)
	for id := 1; id <= 50; id++ {
		if err := unlimited.Add(id, make([]byte, 1000)); err != nil {
			t.Fatalf("Add %d under unlimited budget: %v", id, err)
		}
	}
	if err := unlimited.reconcile(); err != nil {
		t.Fatalf("reconcile under unlimited budget: %v", err)
	}
	for id := 1; id <= 50; id++ {
		if !unlimited.Has(id) {
			t.Fatalf("id %d evicted despite unlimited budget", id)
		}
	}

	dc := newTestDiskCapture(t, 20)
	if err := dc.Add(1, make([]byte, 21)); !errors.Is(err, errExceedsCaptureMax) {
		t.Fatalf("Add oversized err = %v, want errExceedsCaptureMax", err)
	}
	if dc.Has(1) {
		t.Fatal("oversized capture was stored")
	}
	for id := 1; id <= 3; id++ { // 30 bytes over a 20 budget
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
	if data, err := dc.Get(7); err != nil || string(data) != "two-and-longer" {
		t.Fatalf("Get = %q, %v, want overwritten value", data, err)
	}
}

// TestCaptureDisk_ShardsByThousandFiles checks the on-disk layout: ids are
// grouped into decimal folders of 1000 (id/1000) and nothing lands at the top
// level.
func TestCaptureDisk_ShardsByThousandFiles(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)
	cases := map[int]string{
		7:      "0/7.cap",
		999:    "0/999.cap",
		1000:   "1/1000.cap",
		1050:   "1/1050.cap",
		42_501: "42/42501.cap",
	}
	for id, rel := range cases {
		if err := dc.Add(id, []byte("x")); err != nil {
			t.Fatalf("Add %d: %v", id, err)
		}
		want := filepath.Join(dc.ns, filepath.FromSlash(rel))
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("id %d not at %s: %v", id, rel, err)
		}
		if got := dc.path(id); got != want {
			t.Fatalf("path(%d) = %s, want %s", id, got, want)
		}
	}
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

// TestCaptureDisk_NoEvictionOrScanAtOpen proves reopening is O(1): over-budget
// files from a previous run all survive until reconcile runs, and every one is
// found purely by probing the exact sharded path (lazy discovery).
func TestCaptureDisk_NoEvictionOrScanAtOpen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	dc, err := newDiskCaptureOpts(dir, 30, "db", nil, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := dc.Add(1050, []byte("persisted")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	for id := 1; id <= 5; id++ {
		if err := dc.Add(id, make([]byte, 10)); err != nil { // 59 bytes over a 30 budget
			t.Fatalf("Add %d: %v", id, err)
		}
	}
	dc.Close()

	dc2, err := newDiskCaptureOpts(dir, 30, "db", nil, false)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer dc2.Close()
	if data, err := dc2.Get(1050); err != nil || string(data) != "persisted" {
		t.Fatalf("lazy discovery after reopen: %q, %v", data, err)
	}
	for id := 1; id <= 5; id++ {
		if !dc2.Has(id) {
			t.Fatalf("id %d evicted at open: reopening must not scan or evict", id)
		}
	}

	// One reconcile pass drops the oldest ids until the budget fits: ids 1-3
	// go, leaving ids 4-5 plus "persisted" (9 bytes) at 29 <= 30.
	if err := dc2.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, id := range []int{1, 2, 3} {
		if dc2.Has(id) {
			t.Fatalf("id %d should have been evicted", id)
		}
	}
	for _, id := range []int{4, 5, 1050} {
		if !dc2.Has(id) {
			t.Fatalf("id %d should be retained after reconcile", id)
		}
	}
}

// TestCaptureDisk_ReconcileLoopEvictsInBackground exercises the goroutine the
// production constructor starts: writes signal a background pass that evicts
// the oldest ids without an explicit reconcile call.
func TestCaptureDisk_ReconcileLoopEvictsInBackground(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	dc, err := newDiskCapture(dir, 20, "db", nil) // startLoop = true
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

// TestCaptureDisk_TieredStore covers the two-tier behavior: Add writes both
// tiers, an item too large for memory lands on disk only, and reads fall back
// to disk once memory has evicted the entry.
func TestCaptureDisk_TieredStore(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)
	mem := cache.New(16)
	layered := combineCapture(logmon.NewWriter(io.Discard), mem, dc)

	if err := layered.Add(1, []byte("payload")); err != nil {
		t.Fatalf("tiered Add: %v", err)
	}
	if err := layered.Add(2, make([]byte, 4096)); err != nil {
		t.Fatalf("tiered Add oversized-for-mem: %v", err)
	}
	if !mem.Has(1) || !dc.Has(1) {
		t.Fatal("tiered Add must write to both tiers")
	}
	if mem.Has(2) || !dc.Has(2) {
		t.Fatal("item too large for memory must be retained on disk only")
	}

	mem.Clear()
	if !layered.Has(1) {
		t.Fatal("Has must report disk tier after memory eviction")
	}
	if data, err := layered.Get(1); err != nil || string(data) != "payload" {
		t.Fatalf("fallback Get = %q, %v", data, err)
	}
}

// TestCaptureDisk_CloseStopsLoop proves Close terminates the background
// reconcile goroutine (waited on, not just signaled) and is idempotent, so
// reloads do not leak reconcilers.
func TestCaptureDisk_CloseStopsLoop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	dc, err := newDiskCapture(dir, 1<<20, "db", nil) // loop started
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
// the metricsMonitor is retrievable by a fresh monitor over the same dir via
// the lazy disk path, i.e. it survives a process restart (which the
// in-memory-only path cannot) without any boot-time scan.
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

// TestCaptureDisk_MonitorDiskTierGating verifies the disk tier is built only
// with both a captureDir and a persistent activity store: IDs must survive
// restarts (an in-memory DB resets every boot and would orphan capture files),
// and captureDir — not the byte budget — is the enable switch.
func TestCaptureDisk_MonitorDiskTierGating(t *testing.T) {
	memStore, err := store.New("")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer memStore.Close()
	dir := t.TempDir()
	persistStore, err := store.New(filepath.Join(dir, "activity.sqlite"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer persistStore.Close()

	cases := map[string]struct {
		st      *store.Store
		diskDir string
	}{
		"in-memory store":  {st: memStore, diskDir: filepath.Join(t.TempDir(), "caps")},
		"empty captureDir": {st: persistStore, diskDir: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mm := newMetricsMonitorWithDisk(logmon.NewWriter(io.Discard), 0, 0, 512, tc.diskDir, tc.st)
			defer mm.Close()
			if mm.enableCaptures || mm.captureCache != nil {
				t.Fatalf("disk tier must be disabled with an %s", name)
			}
		})
	}
}

// TestCaptureDisk_ReconcileNotOverEvictedByRacingAdd guards the eviction
// budget against writes racing the reconcile walk: a capture added mid-walk
// (counted both by Add and missed by the walk) must not inflate the total the
// eviction loop uses, or older captures that still fit get deleted.
func TestCaptureDisk_ReconcileNotOverEvictedByRacingAdd(t *testing.T) {
	dc := newTestDiskCapture(t, 100)
	for id := 1; id <= 4; id++ { // 104 bytes over a 100 budget: exactly id 1 must go
		if err := dc.Add(id, make([]byte, 26)); err != nil {
			t.Fatalf("Add %d: %v", id, err)
		}
	}

	walkStarted := make(chan struct{})
	release := make(chan struct{})
	dc.onWalkStart = func() { close(walkStarted); <-release }

	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- dc.reconcile() }()

	<-walkStarted
	addDone := make(chan error, 1)
	go func() { addDone <- dc.Add(5, make([]byte, 40)) }()
	time.Sleep(50 * time.Millisecond) // let the racing Add reach the store
	close(release)

	if err := <-reconcileDone; err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := <-addDone; err != nil {
		t.Fatalf("racing Add: %v", err)
	}

	if dc.Has(1) {
		t.Fatal("oldest capture (id 1) should be evicted")
	}
	for _, id := range []int{2, 3, 4, 5} {
		if !dc.Has(id) {
			t.Fatalf("id %d evicted: a racing Add must not over-evict captures that still fit", id)
		}
	}
}

// TestCaptureDisk_TmpWriteDoesNotFollowSymlink proves a planted symlink at the
// predictable temp path cannot make the store write through to another file.
func TestCaptureDisk_TmpWriteDoesNotFollowSymlink(t *testing.T) {
	dc := newTestDiskCapture(t, 1<<20)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("secret"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	if err := os.MkdirAll(dc.shardDir(9), 0o700); err != nil {
		t.Fatalf("shard dir: %v", err)
	}
	if err := os.Symlink(victim, dc.path(9)+".tmp"); err != nil {
		t.Fatalf("plant symlink: %v", err)
	}

	if err := dc.Add(9, []byte("capture")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "secret" {
		t.Fatalf("temp write followed a symlink: victim = %q, %v", data, err)
	}
	if data, err := dc.Get(9); err != nil || string(data) != "capture" {
		t.Fatalf("Get = %q, %v, want %q", data, err, "capture")
	}
}

// TestCaptureDisk_ReconcileRemovesCrashTmpFiles proves temp files left by a
// crashed Add (both the current os.CreateTemp pattern and the legacy
// <id>.cap.tmp name) are dropped by the next walk: they must not linger and
// must not count against the budget.
func TestCaptureDisk_ReconcileRemovesCrashTmpFiles(t *testing.T) {
	dc := newTestDiskCapture(t, 100)
	if err := dc.Add(1, make([]byte, 10)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	shard := dc.shardDir(2)
	if err := os.MkdirAll(shard, 0o700); err != nil {
		t.Fatalf("shard dir: %v", err)
	}
	leftovers := []string{
		filepath.Join(shard, ".tmp-crashed"),
		filepath.Join(shard, "2.cap.tmp"),
	}
	for _, p := range leftovers {
		if err := os.WriteFile(p, make([]byte, 90), 0o600); err != nil {
			t.Fatalf("plant leftover %s: %v", p, err)
		}
	}

	if err := dc.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, p := range leftovers {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("crash leftover %s survived reconcile: %v", p, err)
		}
	}
	if !dc.Has(1) {
		t.Fatal("capture evicted despite fitting the budget once leftovers are gone")
	}
	if got := dc.total.Load(); got != 10 {
		t.Fatalf("total = %d, want 10: leftover bytes must not be counted", got)
	}
}

// TestCaptureDisk_ReconcileKeepsTotalOnWalkError guards the authoritative
// total: a walk that aborts on an unreadable shard saw only part of the store,
// so its partial sum must not replace the running counter, and eviction must
// not run from it.
func TestCaptureDisk_ReconcileKeepsTotalOnWalkError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	dc, err := newDiskCaptureOpts(dir, 100, "db", nil, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer dc.Close()
	if err := dc.Add(1, make([]byte, 10)); err != nil {
		t.Fatalf("Add 1: %v", err)
	}
	if err := dc.Add(1000, make([]byte, 10)); err != nil {
		t.Fatalf("Add 1000: %v", err)
	}

	shard := dc.shardDir(1000)
	if err := os.Chmod(shard, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(shard, 0o700) })
	if _, err := os.ReadDir(shard); err == nil {
		t.Skip("filesystem ignores directory permissions (running as root?)")
	}

	dc.total.Store(999) // the pre-walk running counter
	err = dc.reconcile()
	if err == nil {
		t.Fatal("reconcile must report an unreadable shard")
	}
	if got := dc.total.Load(); got != 999 {
		t.Fatalf("total = %d, want 999: a partial walk must not overwrite the authoritative total", got)
	}
	if !dc.Has(1) {
		t.Fatal("id 1 evicted: a failed walk must not evict from its partial sum")
	}
}

// TestCaptureDisk_WarnsOnSharedWritableDir verifies opening an existing capture
// dir that group/others can write to is reported: local users could then plant
// the files the store reads back.
func TestCaptureDisk_WarnsOnSharedWritableDir(t *testing.T) {
	var buf bytes.Buffer
	logger := logmon.NewWriter(&buf)

	shared := filepath.Join(t.TempDir(), "shared")
	if _, err := newDiskCaptureOpts(shared, 1<<20, "db", logger, false); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := os.Chmod(shared, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if info, err := os.Stat(shared); err != nil {
		t.Fatalf("stat: %v", err)
	} else if info.Mode().Perm()&0o077 != 0 {
		t.Skip("filesystem ignores directory permissions (Windows?)")
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := newDiskCaptureOpts(shared, 1<<20, "db", logger, false); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("writable")) {
		t.Fatalf("no warning for shared-writable capture dir, log = %q", buf.Bytes())
	}

	buf.Reset()
	private := filepath.Join(t.TempDir(), "private")
	if _, err := newDiskCaptureOpts(private, 1<<20, "db", logger, false); err != nil {
		t.Fatalf("open private: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("private capture dir must not warn, log = %q", buf.Bytes())
	}
}

// TestCaptureDisk_CapturesSurviveIdentityChange covers the store identity
// binding: same-id reopens keep captures; an identity change — the activity
// database was swapped or recreated, so ids restart at 1 — keeps the old
// captures side by side (unreachable, still under budget) instead of deleting
// them; root-level files from before namespacing are adopted into the
// current database's namespace.
func TestCaptureDisk_CapturesSurviveIdentityChange(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")

	dc, err := newDiskCaptureOpts(dir, 0, "one", nil, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := dc.Add(1, []byte("old database")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	dc.Close()

	same, err := newDiskCaptureOpts(dir, 0, "one", nil, false)
	if err != nil {
		t.Fatalf("reopen same identity: %v", err)
	}
	if data, err := same.Get(1); err != nil || string(data) != "old database" {
		t.Fatalf("Get = %q, %v, want %q", data, err, "old database")
	}
	same.Close()

	swapped, err := newDiskCaptureOpts(dir, 1<<20, "two", nil, false)
	if err != nil {
		t.Fatalf("reopen swapped identity: %v", err)
	}
	if err := swapped.Add(1, []byte("new database")); err != nil {
		t.Fatalf("Add after swap: %v", err)
	}
	if data, err := swapped.Get(1); err != nil || string(data) != "new database" {
		t.Fatalf("Get after swap = %q, %v: id 1 must not expose old data", data, err)
	}
	oldFile := filepath.Join(dir, namespacePrefix+"one", "0", "1.cap")
	if data, err := os.ReadFile(oldFile); err != nil || string(data) != "old database" {
		t.Fatalf("old capture = %q, %v, want it kept at %s", data, err, oldFile)
	}
	swapped.Close()

	// Flipping back to the old database finds its captures intact.
	back, err := newDiskCaptureOpts(dir, 1<<20, "one", nil, false)
	if err != nil {
		t.Fatalf("reopen old identity: %v", err)
	}
	defer back.Close()
	if data, err := back.Get(1); err != nil || string(data) != "old database" {
		t.Fatalf("Get after flip back = %q, %v, want %q", data, err, "old database")
	}

	// The byte budget spans generations: an over-budget store evicts the
	// older database's files before the current one's.
	cross := filepath.Join(t.TempDir(), "cross")
	a, err := newDiskCaptureOpts(cross, 1000, "ga", nil, false)
	if err != nil {
		t.Fatalf("open gen-a: %v", err)
	}
	if err := a.Add(1, make([]byte, 100)); err != nil {
		t.Fatalf("gen-a Add: %v", err)
	}
	a.Close()
	b, err := newDiskCaptureOpts(cross, 1000, "gb", nil, false)
	if err != nil {
		t.Fatalf("open gen-b: %v", err)
	}
	defer b.Close()
	if err := b.Add(1, make([]byte, 500)); err != nil {
		t.Fatalf("gen-b Add 1: %v", err)
	}
	if err := b.Add(2, make([]byte, 500)); err != nil {
		t.Fatalf("gen-b Add 2: %v", err)
	}
	if err := b.reconcile(); err != nil {
		t.Fatalf("cross-generation reconcile: %v", err)
	}
	if !b.Has(1) || !b.Has(2) {
		t.Fatal("current database evicted while an older generation still had room to give")
	}
	if _, err := os.Stat(filepath.Join(cross, namespacePrefix+"ga", "0", "1.cap")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest generation must be evicted first: %v", err)
	}

	// Root-level files from before namespacing are adopted into the current
	// database's namespace at first open.
	legacyDir := filepath.Join(t.TempDir(), "legacy")
	if err := os.MkdirAll(filepath.Join(legacyDir, "0"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "0", "7.cap"), []byte("adopted"), 0o600); err != nil {
		t.Fatalf("write legacy capture: %v", err)
	}
	adopted, err := newDiskCaptureOpts(legacyDir, 0, "nine", nil, false)
	if err != nil {
		t.Fatalf("adopt open: %v", err)
	}
	defer adopted.Close()
	if !adopted.Has(7) {
		t.Fatal("existing captures must be adopted into the namespace")
	}
	if _, err := os.Stat(filepath.Join(legacyDir, namespacePrefix+"nine", "0", "7.cap")); err != nil {
		t.Fatalf("adopted capture not relocated into its namespace: %v", err)
	}
}
