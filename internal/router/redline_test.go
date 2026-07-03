package router

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/process"
)

// liveReadOverResidents wires g.liveRead to the resident set, so enforceRedline
// observes memory dropping as victims stop.
func liveReadOverResidents(procs map[string]process.Process, perModelMB map[string]int) func() (int, bool) {
	return func() (int, bool) {
		total := 0
		for id, p := range procs {
			switch p.State() {
			case process.StateStopped, process.StateShutdown:
				continue
			}
			total += perModelMB[id]
		}
		return total, true
	}
}

func TestMemGate_Redline_NoActionUnderRedline(t *testing.T) {
	a := readyProc("a", 100)
	procs := map[string]process.Process{"a": a}

	g := newTestGate(6000, nil, nil)
	g.redlineMB = 8000
	g.liveRead = func() (int, bool) { return 7000, true } // between budget and redline

	g.enforceRedline(procs, gateLog())

	if a.stopCalls.Load() != 0 {
		t.Fatalf("under the red line nothing may be evicted; got %d stops", a.stopCalls.Load())
	}
}

func TestMemGate_Redline_EvictsLRUDownToBudget_SparesMRU(t *testing.T) {
	// Three residents at 5000 each = 15000, red line 8000, budget 6000.
	// The pass must evict a (oldest) then b, and SPARE c (most recently used)
	// even though 5000 is still not under... it is: 5000 <= 6000. Then stop.
	perModel := map[string]int{"a": 5000, "b": 5000, "c": 5000}
	a := readyProc("a", 100)
	b := readyProc("b", 200)
	c := readyProc("c", 300)
	procs := map[string]process.Process{"a": a, "b": b, "c": c}

	g := newTestGate(6000, nil, map[string]int{"a": 5000, "b": 5000, "c": 5000})
	g.redlineMB = 8000
	g.liveRead = liveReadOverResidents(procs, perModel)

	g.enforceRedline(procs, gateLog())

	if a.stopCalls.Load() == 0 || b.stopCalls.Load() == 0 {
		t.Fatal("expected a and b (LRU order) to be evicted")
	}
	if c.stopCalls.Load() != 0 {
		t.Fatal("soft tier must spare the most-recently-used model")
	}
}

func TestMemGate_Redline_SoftTier_NeverEvictsLastModel(t *testing.T) {
	// One resident over the red line: the soft tier spares the MRU, and with a
	// single candidate that IS the MRU, so nothing may be evicted.
	perModel := map[string]int{"a": 9000}
	a := readyProc("a", 100)
	procs := map[string]process.Process{"a": a}

	g := newTestGate(6000, nil, map[string]int{"a": 9000})
	g.redlineMB = 8000
	g.liveRead = liveReadOverResidents(procs, perModel)

	g.enforceRedline(procs, gateLog())

	if a.stopCalls.Load() != 0 {
		t.Fatal("soft tier must not evict the only (most recently used) model")
	}
}

func TestMemGate_Redline_HardTier_EvictsEverything(t *testing.T) {
	// Above the hard line the MRU is no longer spared.
	perModel := map[string]int{"a": 9000}
	a := readyProc("a", 100)
	procs := map[string]process.Process{"a": a}

	g := newTestGate(6000, nil, map[string]int{"a": 9000})
	g.redlineMB = 8000
	g.hardlineMB = 8500
	g.liveRead = liveReadOverResidents(procs, perModel)

	g.enforceRedline(procs, gateLog())

	if a.stopCalls.Load() == 0 {
		t.Fatal("hard tier must evict even the most-recently-used model")
	}
}

func TestMemGate_Redline_HostFloorBreachTriggersEviction(t *testing.T) {
	// GPU usage is fine, but host MemAvailable is under the floor: the
	// watchdog must still evict (UMA: freeing GTT frees host RAM).
	perModel := map[string]int{"a": 3000, "b": 3000}
	a := readyProc("a", 100)
	b := readyProc("b", 200)
	procs := map[string]process.Process{"a": a, "b": b}

	g := newTestGate(10000, nil, map[string]int{"a": 3000, "b": 3000})
	g.redlineMB = 12000
	g.hostFloorMB = 2000
	g.liveRead = liveReadOverResidents(procs, perModel)

	hostAvail := 1500 // below floor
	g.hostAvail = func() (int, bool) { return hostAvail, true }

	// Freeing a model recovers host memory in this fake.
	stopped := func() int {
		n := 0
		if a.stopCalls.Load() > 0 {
			n++
		}
		if b.stopCalls.Load() > 0 {
			n++
		}
		return n
	}
	g.hostAvail = func() (int, bool) { return hostAvail + stopped()*3000, true }

	g.enforceRedline(procs, gateLog())

	if a.stopCalls.Load() == 0 {
		t.Fatal("expected LRU eviction on host floor breach")
	}
	if b.stopCalls.Load() != 0 {
		t.Fatal("one eviction recovers the floor; MRU must be spared")
	}
}

func TestMemGate_Redline_ReservationHoldersExcluded(t *testing.T) {
	perModel := map[string]int{"a": 5000, "b": 5000}
	a := readyProc("a", 100) // oldest, but holds a reservation
	b := readyProc("b", 200)
	procs := map[string]process.Process{"a": a, "b": b}

	g := newTestGate(4000, nil, map[string]int{"a": 5000, "b": 5000})
	g.redlineMB = 8000
	g.hardlineMB = 8500 // hard: even MRU is fair game, only reservations protect
	g.liveRead = liveReadOverResidents(procs, perModel)
	g.reservations = map[string]int{"a": 5000}

	g.enforceRedline(procs, gateLog())

	if a.stopCalls.Load() != 0 {
		t.Fatal("reservation holder must never be evicted by the watchdog")
	}
	if b.stopCalls.Load() == 0 {
		t.Fatal("expected b to be evicted")
	}
}

func TestMemGate_Redline_NoFreshReader_NoAction(t *testing.T) {
	a := readyProc("a", 100)
	procs := map[string]process.Process{"a": a}

	g := newTestGate(6000, staticProbe(99999, true), nil)
	g.redlineMB = 8000
	g.liveRead = nil // stale probe alone must not drive enforcement

	g.enforceRedline(procs, gateLog())

	if a.stopCalls.Load() != 0 {
		t.Fatal("watchdog must stay dormant without a fresh reader")
	}
}
