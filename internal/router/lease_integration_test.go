package router

import (
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/process"
)

// leaseTableFor returns a lease table with a real-clock now for integration
// tests that only need "live" leases (hour-long TTLs never expire mid-test).
func leaseTableFor(t *testing.T) *leaseTable {
	t.Helper()
	lt := newLeaseTable(4*time.Hour, "")
	t.Cleanup(lt.stop)
	return lt
}

func TestMemGate_SkipsLeasedVictim(t *testing.T) {
	perModel := map[string]int{"leased-lru": 5000, "idle": 5000}
	leased := readyProc("leased-lru", 100) // oldest -> would be the LRU victim
	idle := readyProc("idle", 200)
	procs := map[string]process.Process{"leased-lru": leased, "idle": idle}

	g := newTestGate(6000, nil, nil)
	g.probe = residentProbe(procs, perModel)
	lt := leaseTableFor(t)
	lt.Acquire("leased-lru", "batch.py", "overnight", time.Hour)
	g.leases = lt

	g.EnsureFits("incoming", procs, nil, gateLog())

	if leased.stopCalls.Load() != 0 {
		t.Fatalf("leased LRU model must not be evicted; stops=%d", leased.stopCalls.Load())
	}
	if idle.stopCalls.Load() != 1 {
		t.Fatalf("idle non-leased model should be evicted instead; stops=%d", idle.stopCalls.Load())
	}
}

func TestMemGate_HardlineCanEvictLeasedVictim(t *testing.T) {
	leased := readyProc("leased-lru", 100)
	idle := readyProc("idle", 200)
	procs := map[string]process.Process{"leased-lru": leased, "idle": idle}
	g := newTestGate(1000, nil, nil)
	lt := leaseTableFor(t)
	lt.Acquire("leased-lru", "batch.py", "overnight", time.Hour)
	g.leases = lt

	// allowInFlight==true is the hard-line/host-OOM tier: leases are ignored.
	victim, ok := g.pickLRUVictim(procs, nil, false, true)
	if !ok || victim != "leased-lru" {
		t.Fatalf("hard-line selection must be able to pick a leased model; victim=%q ok=%v", victim, ok)
	}
}

func TestMemGate_StrictRejectionNamesBlockingLease(t *testing.T) {
	// Budget too small: the only resident model is leased, so a strict load
	// cannot free room and must reject with the blocking lease named.
	perModel := map[string]int{"leased": 5000}
	leased := readyProc("leased", 100)
	procs := map[string]process.Process{"leased": leased}

	g := newTestGate(4000, nil, map[string]int{"incoming": 3000})
	g.strict = true
	g.maxWait = 0
	g.probe = residentProbe(procs, perModel)
	lt := leaseTableFor(t)
	lt.Acquire("leased", "batch.py", "important run", time.Hour)
	g.leases = lt

	err := g.EnsureFits("incoming", procs, nil, gateLog())
	if err == nil {
		t.Fatal("strict admission should reject when all candidates are leased")
	}
	be, ok := err.(*MemoryBudgetError)
	if !ok {
		t.Fatalf("want *MemoryBudgetError, got %T", err)
	}
	if len(be.BlockedBy) != 1 || be.BlockedBy[0].Model != "leased" || be.BlockedBy[0].Holder != "batch.py" {
		t.Fatalf("rejection must name the blocking lease; got %+v", be.BlockedBy)
	}
}

func TestBaseRouter_TryClaimEviction_RefusesLeased(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	b := newSerializeTestBase(t, map[string]process.Process{"a": a}, nil)
	b.leases.Acquire("a", "holder", "reason", time.Hour)

	err := b.TryClaimEviction([]string{"a"})
	if err == nil {
		t.Fatal("claiming a leased model for eviction must be refused")
	}
	if _, ok := err.(*LeaseBlockedError); !ok {
		t.Fatalf("want *LeaseBlockedError, got %T", err)
	}

	// An unleased model claims cleanly, and releasing lets a later lease land.
	if err := b.TryClaimEviction([]string{"b-model"}); err != nil {
		t.Fatalf("unleased claim should succeed; got %v", err)
	}
	b.leases.ReleaseEvictionClaim([]string{"b-model"})
}

func TestBaseRouter_LeaseHeaderValidation(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	bp := newFakeProcess("b")
	bp.markReady()
	b := newSerializeTestBase(t, map[string]process.Process{"a": a, "b": bp}, nil)
	lease, _ := b.leases.Acquire("a", "holder", "reason", time.Hour)

	// Header naming a lease for "a" while the request targets "b" is a mismatch.
	rMismatch := newRequest("b")
	rMismatch.Header.Set(LeaseHeader, lease.ID)
	if err := b.validateLeaseHeader(rMismatch, "b"); err == nil {
		t.Fatal("mismatched lease/model must be rejected")
	} else if _, ok := err.(*LeaseMismatchError); !ok {
		t.Fatalf("want *LeaseMismatchError, got %T", err)
	}

	// Matching model is accepted.
	rMatch := newRequest("a")
	rMatch.Header.Set(LeaseHeader, lease.ID)
	if err := b.validateLeaseHeader(rMatch, "a"); err != nil {
		t.Fatalf("matching lease/model must pass; got %v", err)
	}

	// Unknown lease id is ignored (client re-acquires), not rejected.
	rUnknown := newRequest("a")
	rUnknown.Header.Set(LeaseHeader, "NONEXISTENT")
	if err := b.validateLeaseHeader(rUnknown, "a"); err != nil {
		t.Fatalf("unknown lease id must be ignored; got %v", err)
	}

	// No header is always fine.
	if err := b.validateLeaseHeader(newRequest("a"), "a"); err != nil {
		t.Fatalf("absent header must pass; got %v", err)
	}
}

// TestLeaseTable_ListReleasesLockBeforeInFlight is the regression guard for the
// run-loop deadlock (H1). In production the inFlight callback (baseRouter.InFlight)
// round-trips through the single run loop, and the run loop itself takes the
// lease lock via TryClaimEviction. If List holds t.mu across the callback, the
// two deadlock. This test makes the callback assert it can take t.mu, which is
// only possible if List released it first.
func TestLeaseTable_ListReleasesLockBeforeInFlight(t *testing.T) {
	lt, _ := newTestLeaseTable(t)
	lt.Acquire("m", "holder", "reason", time.Hour)

	done := make(chan struct{})
	go func() {
		lt.List(func(string) int {
			// The lock must be free here; a real inFlight callback needs the run
			// loop, which needs this same lock.
			if !lt.mu.TryLock() {
				t.Error("List held t.mu while calling the inFlight callback (deadlock risk)")
			} else {
				lt.mu.Unlock()
			}
			return 0
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("List deadlocked calling inFlight under its own lock")
	}
}
