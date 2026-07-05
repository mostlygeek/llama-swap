package router

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixedClock returns a controllable now() for deterministic expiry tests.
func fixedClock(start time.Time) (func() time.Time, *time.Time) {
	cur := start
	return func() time.Time { return cur }, &cur
}

func newTestLeaseTable(t *testing.T) (*leaseTable, *time.Time) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nowFn, cur := fixedClock(base)
	lt := newLeaseTable(4*time.Hour, "")
	lt.now = nowFn
	t.Cleanup(lt.stop)
	return lt, cur
}

func TestLeaseTable_AcquireReleaseProtects(t *testing.T) {
	lt, _ := newTestLeaseTable(t)

	l, err := lt.Acquire("m1", "runner.py", "batch", time.Hour)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if l.ID == "" || l.Model != "m1" || l.State != LeaseActive {
		t.Fatalf("unexpected lease: %+v", l)
	}
	if !lt.IsProtected("m1") {
		t.Fatal("m1 should be protected while a live lease exists")
	}
	if lt.IsProtected("m2") {
		t.Fatal("m2 has no lease and must not be protected")
	}

	if !lt.Release(l.ID) {
		t.Fatal("release of a known id should return true")
	}
	if lt.IsProtected("m1") {
		t.Fatal("m1 must not be protected after release")
	}
	if lt.Release(l.ID) {
		t.Fatal("second release of the same id should return false")
	}
}

func TestLeaseTable_Refcounting(t *testing.T) {
	lt, _ := newTestLeaseTable(t)
	a, _ := lt.Acquire("m", "A", "", time.Hour)
	b, _ := lt.Acquire("m", "B", "", time.Hour)

	lt.Release(a.ID)
	if !lt.IsProtected("m") {
		t.Fatal("model must stay protected while B's lease is live")
	}
	lt.Release(b.ID)
	if lt.IsProtected("m") {
		t.Fatal("model must be unprotected after the last lease is released")
	}
}

func TestLeaseTable_ExpiryIsAuthoritativeWithoutSweep(t *testing.T) {
	lt, cur := newTestLeaseTable(t)
	lt.Acquire("m", "A", "", 10*time.Minute)

	*cur = cur.Add(9 * time.Minute)
	if !lt.IsProtected("m") {
		t.Fatal("still protected before expiry")
	}
	*cur = cur.Add(2 * time.Minute) // now past expiry, without any sweep
	if lt.IsProtected("m") {
		t.Fatal("expired lease must not protect even before the sweeper runs")
	}
	if got := lt.List(nil); len(got) != 0 {
		t.Fatalf("expired lease must not appear in List; got %d", len(got))
	}
}

func TestLeaseTable_TTLClampedToMax(t *testing.T) {
	lt, cur := newTestLeaseTable(t)
	l, _ := lt.Acquire("m", "A", "", 10*time.Hour) // cap is 4h
	want := cur.Add(4 * time.Hour)
	if !l.ExpiresAt.Equal(want) {
		t.Fatalf("ttl not clamped: expires=%v want=%v", l.ExpiresAt, want)
	}

	// ttl <= 0 also means "the maximum".
	l2, _ := lt.Acquire("m2", "A", "", 0)
	if !l2.ExpiresAt.Equal(cur.Add(4 * time.Hour)) {
		t.Fatalf("zero ttl should map to the cap; got %v", l2.ExpiresAt)
	}
}

func TestLeaseTable_ExtendCannotExceedCap(t *testing.T) {
	lt, cur := newTestLeaseTable(t)
	l, _ := lt.Acquire("m", "A", "", time.Hour)
	*cur = cur.Add(30 * time.Minute)

	ext, ok := lt.Extend(l.ID, 10*time.Hour)
	if !ok {
		t.Fatal("extend of a live lease should succeed")
	}
	want := cur.Add(4 * time.Hour)
	if !ext.ExpiresAt.Equal(want) {
		t.Fatalf("extend must clamp to cap: got %v want %v", ext.ExpiresAt, want)
	}

	*cur = cur.Add(5 * time.Hour) // now expired
	if _, ok := lt.Extend(l.ID, time.Hour); ok {
		t.Fatal("extend of an expired lease must fail")
	}
}

func TestLeaseTable_Kill(t *testing.T) {
	lt, _ := newTestLeaseTable(t)
	lt.Acquire("m1", "A", "", time.Hour)
	lt.Acquire("m1", "B", "", time.Hour)
	lt.Acquire("m2", "A", "", time.Hour)

	killed := lt.Kill("", "m1", "")
	if len(killed) != 2 {
		t.Fatalf("kill by model should remove both m1 leases; got %d", len(killed))
	}
	if lt.IsProtected("m1") {
		t.Fatal("m1 must be unprotected after kill")
	}
	if !lt.IsProtected("m2") {
		t.Fatal("m2 must remain protected")
	}

	killed = lt.Kill("", "", "A")
	if len(killed) != 1 || killed[0].Model != "m2" {
		t.Fatalf("kill by holder should remove the remaining A lease; got %+v", killed)
	}
}

func TestLeaseTable_ClaimBlockedByLease(t *testing.T) {
	lt, _ := newTestLeaseTable(t)
	lt.Acquire("m", "A", "batch", time.Hour)

	blockers := lt.TryClaimEviction([]string{"m", "other"})
	if len(blockers) != 1 || blockers[0].Model != "m" || blockers[0].Holder != "A" {
		t.Fatalf("claim of a leased model must be blocked with the holder; got %+v", blockers)
	}
	// Nothing should have been claimed on a blocked attempt.
	if lt.evicting["m"] != 0 || lt.evicting["other"] != 0 {
		t.Fatalf("a blocked claim must not mark anything evicting; got %v", lt.evicting)
	}
}

func TestLeaseTable_ClaimThenAcquireRace(t *testing.T) {
	lt, _ := newTestLeaseTable(t)

	// Model is clear: eviction wins the claim.
	if blockers := lt.TryClaimEviction([]string{"m"}); blockers != nil {
		t.Fatalf("claim of an unleased model should succeed; got %+v", blockers)
	}
	// A lease acquire on a claimed (mid-eviction) model must fail: eviction won.
	if _, err := lt.Acquire("m", "late", "", time.Hour); err == nil {
		t.Fatal("acquire on an eviction-claimed model must fail")
	}
	// After the eviction completes, acquire works again.
	lt.ReleaseEvictionClaim([]string{"m"})
	if _, err := lt.Acquire("m", "later", "", time.Hour); err != nil {
		t.Fatalf("acquire after claim release should succeed; got %v", err)
	}
}

func TestLeaseTable_AcquireThenClaimRace(t *testing.T) {
	lt, _ := newTestLeaseTable(t)
	// Lease acquired first: a subsequent eviction claim must be refused.
	lt.Acquire("m", "holder", "reason", time.Hour)
	if blockers := lt.TryClaimEviction([]string{"m"}); len(blockers) != 1 {
		t.Fatalf("claim must be refused once a lease exists; got %+v", blockers)
	}
}

func TestLeaseTable_ClaimRefcount(t *testing.T) {
	lt, _ := newTestLeaseTable(t)
	lt.TryClaimEviction([]string{"m"})
	lt.TryClaimEviction([]string{"m"}) // second concurrent claim on same model
	lt.ReleaseEvictionClaim([]string{"m"})
	if _, err := lt.Acquire("m", "x", "", time.Hour); err == nil {
		t.Fatal("model must remain claimed until every claim is released")
	}
	lt.ReleaseEvictionClaim([]string{"m"})
	if _, err := lt.Acquire("m", "x", "", time.Hour); err != nil {
		t.Fatalf("acquire should work after all claims released; got %v", err)
	}
}

func TestLeaseTable_ListAnnotatesInFlight(t *testing.T) {
	lt, _ := newTestLeaseTable(t)
	lt.Acquire("m", "A", "r", time.Hour)
	views := lt.List(func(model string) int {
		if model == "m" {
			return 3
		}
		return 0
	})
	if len(views) != 1 || views[0].ActiveRequests != 3 || views[0].TTLRemainingMS <= 0 {
		t.Fatalf("list view not annotated: %+v", views)
	}
}

func TestLeaseTable_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leases.json")

	lt := newLeaseTable(4*time.Hour, path)
	l, _ := lt.Acquire("m", "A", "batch", time.Hour)
	// Give the writer goroutine a moment, then stop to flush.
	waitFor(t, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
	lt.stop()

	// Reload into a fresh table.
	lt2 := newLeaseTable(4*time.Hour, path)
	t.Cleanup(lt2.stop)
	if err := lt2.loadAndReconcile(path); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, ok := lt2.Lookup(l.ID)
	if !ok || got.Model != "m" || got.Holder != "A" {
		t.Fatalf("persisted lease not restored: %+v ok=%v", got, ok)
	}
}

func TestLeaseTable_PersistenceDropsExpiredOnReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leases.json")
	// Hand-write an already-expired lease.
	writeLeaseFile(path, []Lease{{
		ID: "OLD", Model: "m", Holder: "A", State: LeaseActive,
		AcquiredAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt:  time.Now().Add(-time.Hour),
	}})

	lt := newLeaseTable(4*time.Hour, path)
	t.Cleanup(lt.stop)
	if err := lt.loadAndReconcile(path); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := lt.Lookup("OLD"); ok {
		t.Fatal("expired lease must be dropped on reload")
	}
}

func TestNewLeaseID_SortableAndUnique(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := newLeaseID(base)
	b := newLeaseID(base.Add(time.Millisecond))
	if len(a) != 26 || len(b) != 26 {
		t.Fatalf("ULID must be 26 chars; got %d and %d", len(a), len(b))
	}
	if a >= b {
		t.Fatalf("later timestamp must sort after earlier: %s !< %s", a, b)
	}
	seen := map[string]bool{}
	for range 1000 {
		id := newLeaseID(base)
		if seen[id] {
			t.Fatalf("duplicate id generated: %s", id)
		}
		seen[id] = true
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
