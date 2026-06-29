package router

import (
	"io"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// fakeProbe is a memProbe whose reading is computed on each call, so a test can
// model GPU memory shrinking as victims are stopped.
type fakeProbe struct {
	read func() (int, bool)
}

func (p fakeProbe) liveUsedMB() (int, bool) { return p.read() }

// staticProbe always reports the same usage.
func staticProbe(usedMB int, ok bool) fakeProbe {
	return fakeProbe{read: func() (int, bool) { return usedMB, ok }}
}

// residentProbe reports per-model MiB summed over models that are still
// resident (not stopped/shutdown). This lets EnsureFits's post-Stop re-read see
// freed memory, exercising the real eviction loop.
func residentProbe(procs map[string]process.Process, perModelMB map[string]int) fakeProbe {
	return fakeProbe{read: func() (int, bool) {
		total := 0
		for id, p := range procs {
			switch p.State() {
			case process.StateStopped, process.StateShutdown:
				continue
			}
			total += perModelMB[id]
		}
		return total, true
	}}
}

// gateLog returns a discard logger for memgate tests (the package-level
// testLogger writes to stdout; the gate's debug/info lines would be noisy).
func gateLog() *logmon.Monitor { return logmon.NewWriter(io.Discard) }

// readyProc returns a fakeProcess in the ready (resident) state with a given
// LRU timestamp.
func readyProc(id string, lastUseNano int64) *fakeProcess {
	p := newFakeProcess(id)
	p.setState(process.StateReady)
	p.lastUse.Store(lastUseNano)
	return p
}

func newTestGate(budgetMB int, probe memProbe, estimates map[string]int) *memGate {
	return &memGate{
		budgetMB:    budgetMB,
		probe:       probe,
		estimates:   estimates,
		stopTimeout: time.Second,
	}
}

func TestMemGate_NoOp_WhenBudgetZero(t *testing.T) {
	a := readyProc("a", 1)
	procs := map[string]process.Process{"a": a}
	// budget 0 => the gate is disabled even though usage is huge.
	g := newTestGate(0, staticProbe(99999, true), nil)

	g.EnsureFits("b", procs, nil, gateLog())

	if a.stopCalls.Load() != 0 {
		t.Fatalf("budget==0 must be a no-op; got %d stops", a.stopCalls.Load())
	}
}

func TestMemGate_NoOp_WhenGateNil(t *testing.T) {
	// A nil *memGate must be safe to call (matches the `if b.memGate != nil`
	// guard being optional from the gate's own perspective).
	var g *memGate
	g.EnsureFits("b", map[string]process.Process{}, nil, gateLog())
}

func TestMemGate_NoOp_WhenNewMemGateDisabled(t *testing.T) {
	// newMemGate returns nil when budget<=0 or monitor is nil.
	if newMemGate(0, nil, nil, time.Second) != nil {
		t.Fatal("expected nil gate for budget 0")
	}
	if newMemGate(1000, nil, nil, time.Second) != nil {
		t.Fatal("expected nil gate for nil monitor")
	}
}

func TestMemGate_FailOpen_WhenNoReading(t *testing.T) {
	a := readyProc("a", 1)
	procs := map[string]process.Process{"a": a}
	// Probe reports "not ok": gate must fail open and evict nothing.
	g := newTestGate(100, staticProbe(0, false), nil)

	g.EnsureFits("b", procs, nil, gateLog())

	if a.stopCalls.Load() != 0 {
		t.Fatalf("no reading must fail open; got %d stops", a.stopCalls.Load())
	}
}

func TestMemGate_FitsWithoutEviction(t *testing.T) {
	a := readyProc("a", 1)
	procs := map[string]process.Process{"a": a}
	// used 40 + estimate 50 = 90 <= budget 100: nothing should be evicted.
	g := newTestGate(100, staticProbe(40, true), map[string]int{"b": 50})

	g.EnsureFits("b", procs, nil, gateLog())

	if a.stopCalls.Load() != 0 {
		t.Fatalf("load fits under budget; got %d stops", a.stopCalls.Load())
	}
}

func TestMemGate_EvictsToFit_ReducesProjectedUnderBudget(t *testing.T) {
	// Three resident models, 5000 MiB each, budget 12000. Loading "d"
	// (estimate 5000) projects 15000+5000 over budget; evicting brings it
	// under. residentProbe models the freeing as victims stop.
	perModel := map[string]int{"a": 5000, "b": 5000, "c": 5000}
	a := readyProc("a", 100)
	b := readyProc("b", 200)
	c := readyProc("c", 300)
	procs := map[string]process.Process{"a": a, "b": b, "c": c}

	g := newTestGate(12000, nil, map[string]int{"d": 5000})
	g.probe = residentProbe(procs, perModel)

	g.EnsureFits("d", procs, nil, gateLog())

	// Final projected = live(resident) + estimate(5000) must be <= budget.
	live, _ := g.probe.liveUsedMB()
	if live+5000 > g.budgetMB {
		t.Fatalf("projected %d still over budget %d after eviction", live+5000, g.budgetMB)
	}
	// At least one eviction must have occurred (3*5000 + 5000 = 20000 > 12000).
	totalStops := a.stopCalls.Load() + b.stopCalls.Load() + c.stopCalls.Load()
	if totalStops == 0 {
		t.Fatal("expected at least one eviction")
	}
}

func TestMemGate_LRUOrder_OldestEvictedFirst(t *testing.T) {
	// a is oldest (lastUse 100), then b (200), then c (300). Budget forces
	// exactly one eviction; it must be "a".
	perModel := map[string]int{"a": 5000, "b": 5000, "c": 5000}
	a := readyProc("a", 100)
	b := readyProc("b", 200)
	c := readyProc("c", 300)
	procs := map[string]process.Process{"a": a, "b": b, "c": c}

	// live = 15000, estimate(d)=0 (reactive). budget 11000 => need to drop one
	// 5000 model to reach 10000.
	g := newTestGate(11000, nil, nil)
	g.probe = residentProbe(procs, perModel)

	g.EnsureFits("d", procs, nil, gateLog())

	if a.stopCalls.Load() != 1 {
		t.Fatalf("oldest model a must be evicted first; a stops=%d", a.stopCalls.Load())
	}
	if b.stopCalls.Load() != 0 || c.stopCalls.Load() != 0 {
		t.Fatalf("only the LRU victim should be evicted; b=%d c=%d",
			b.stopCalls.Load(), c.stopCalls.Load())
	}
}

func TestMemGate_LRUOrder_EvictsMultipleOldestFirst(t *testing.T) {
	// Need to evict two of three to fit. Order must be a then b (oldest two),
	// leaving c (newest) resident.
	perModel := map[string]int{"a": 5000, "b": 5000, "c": 5000}
	a := readyProc("a", 100)
	b := readyProc("b", 200)
	c := readyProc("c", 300)
	procs := map[string]process.Process{"a": a, "b": b, "c": c}

	// live 15000, estimate(d) 0, budget 6000 => must drop two (down to 5000).
	g := newTestGate(6000, nil, nil)
	g.probe = residentProbe(procs, perModel)

	g.EnsureFits("d", procs, nil, gateLog())

	if a.stopCalls.Load() != 1 || b.stopCalls.Load() != 1 {
		t.Fatalf("two oldest must be evicted; a=%d b=%d", a.stopCalls.Load(), b.stopCalls.Load())
	}
	if c.stopCalls.Load() != 0 {
		t.Fatalf("newest model c must remain resident; c stops=%d", c.stopCalls.Load())
	}
	if c.State() == process.StateStopped {
		t.Fatal("c should still be resident")
	}
}

func TestMemGate_FailOpen_WhenNothingLeftToEvict(t *testing.T) {
	// Single resident model "a", but it is excluded because it is already in
	// alreadyEvicting. Budget cannot be met; gate must fail open (no panic, no
	// extra stop) and let the load proceed.
	perModel := map[string]int{"a": 5000}
	a := readyProc("a", 100)
	procs := map[string]process.Process{"a": a}

	g := newTestGate(1000, nil, map[string]int{"d": 5000})
	g.probe = residentProbe(procs, perModel)

	// "a" is already being evicted by the solver, so it is not a candidate.
	g.EnsureFits("d", procs, []string{"a"}, gateLog())

	if a.stopCalls.Load() != 0 {
		t.Fatalf("excluded model must not be evicted by the gate; a stops=%d", a.stopCalls.Load())
	}
}

func TestMemGate_OverEviction_ConstantProbeDoesNotEvictEverything(t *testing.T) {
	// H1 regression: the perf monitor polls on a >=5s interval, so immediately
	// after a victim Stop the probe still reports the PRE-eviction value. A gate
	// that re-reads and trusts that stale number would keep `projected` over
	// budget and evict the ENTIRE resident set. constProbe models exactly that
	// (usage never drops). With arithmetic decrement, evicting the single oldest
	// 5000 MiB model must be enough; b and c must remain resident.
	perModel := map[string]int{"a": 5000, "b": 5000, "c": 5000}
	a := readyProc("a", 100)
	b := readyProc("b", 200)
	c := readyProc("c", 300)
	procs := map[string]process.Process{"a": a, "b": b, "c": c}

	// live=15000 (constant, never drops), estimate(d)=0, budget=11000 => one
	// 5000 model must be dropped (15000-5000=10000 <= 11000). A stale-trusting
	// loop would never see the drop and evict all three.
	g := newTestGate(11000, staticProbe(15000, true), map[string]int{
		"a": 5000, "b": 5000, "c": 5000,
	})

	g.EnsureFits("d", procs, nil, gateLog())

	if a.stopCalls.Load() != 1 {
		t.Fatalf("oldest model a must be evicted; a stops=%d", a.stopCalls.Load())
	}
	if b.stopCalls.Load() != 0 || c.stopCalls.Load() != 0 {
		t.Fatalf("only the necessary victim should be evicted, not the whole resident set; b=%d c=%d",
			b.stopCalls.Load(), c.stopCalls.Load())
	}
	_ = perModel
}

func TestMemGate_OverEviction_UnknownSizeVictimBoundsToOneEviction(t *testing.T) {
	// H1 fail-open: a victim with an unknown (0) estimate frees an unknown
	// amount, so arithmetic makes no progress and the stale live re-read can't
	// be trusted. The gate must evict exactly one such victim and then stop,
	// never looping to evict everything.
	a := readyProc("a", 100)
	b := readyProc("b", 200)
	c := readyProc("c", 300)
	procs := map[string]process.Process{"a": a, "b": b, "c": c}

	// No estimates at all (all unknown), constant probe well over budget.
	g := newTestGate(1000, staticProbe(15000, true), nil)

	g.EnsureFits("d", procs, nil, gateLog())

	totalStops := int(a.stopCalls.Load() + b.stopCalls.Load() + c.stopCalls.Load())
	if totalStops != 1 {
		t.Fatalf("unknown-size victim must bound eviction to exactly one (fail-open); got %d stops", totalStops)
	}
	// It must be the oldest (a).
	if a.stopCalls.Load() != 1 {
		t.Fatalf("oldest unknown-size model a should be the single eviction; a stops=%d", a.stopCalls.Load())
	}
}

func TestMemGate_SkipsStartingVictim(t *testing.T) {
	// H2 regression: a model another parallel swap is mid-loading is in
	// StateStarting. It must never be chosen as a victim. Crucially, a
	// never-served Starting model has LastUse()==0, which sorts FIRST, so it
	// would otherwise be the preferred victim.
	perModel := map[string]int{"starting": 5000, "old": 5000}
	starting := readyProc("starting", 0) // never served => LastUse 0, sorts first
	starting.setState(process.StateStarting)
	old := readyProc("old", 100)
	procs := map[string]process.Process{"starting": starting, "old": old}

	// live=10000, estimate(d)=0, budget=6000 => must drop one 5000 model. The
	// only eligible victim is "old"; "starting" must be skipped despite sorting
	// first by LastUse.
	g := newTestGate(6000, nil, nil)
	g.probe = residentProbe(procs, perModel)

	g.EnsureFits("d", procs, nil, gateLog())

	if starting.stopCalls.Load() != 0 {
		t.Fatalf("a StateStarting model (another swap's incoming target) must never be evicted; stops=%d",
			starting.stopCalls.Load())
	}
	if old.stopCalls.Load() != 1 {
		t.Fatalf("the resident, non-transitioning model should be evicted; old stops=%d", old.stopCalls.Load())
	}
}

func TestMemGate_SkipsStoppingVictim(t *testing.T) {
	// H2: a model already being torn down by another swap is in StateStopping.
	// Re-evicting it races that swap and frees nothing new, so it must be
	// skipped.
	perModel := map[string]int{"stopping": 5000, "old": 5000}
	stopping := readyProc("stopping", 50)
	stopping.setState(process.StateStopping)
	old := readyProc("old", 100)
	procs := map[string]process.Process{"stopping": stopping, "old": old}

	g := newTestGate(6000, nil, nil)
	g.probe = residentProbe(procs, perModel)

	g.EnsureFits("d", procs, nil, gateLog())

	if stopping.stopCalls.Load() != 0 {
		t.Fatalf("a StateStopping model must not be re-evicted; stops=%d", stopping.stopCalls.Load())
	}
	if old.stopCalls.Load() != 1 {
		t.Fatalf("the resident model should be evicted; old stops=%d", old.stopCalls.Load())
	}
}

func TestMemGate_DoesNotEvictIncomingOrStopped(t *testing.T) {
	// "incoming" is resident (e.g. a warm restart) and must never be chosen as
	// a victim. A stopped model must also be skipped. Only "old" is evictable.
	perModel := map[string]int{"incoming": 5000, "old": 5000, "dead": 5000}
	incoming := readyProc("incoming", 50) // oldest, but it's the incoming model
	old := readyProc("old", 100)
	dead := readyProc("dead", 10) // oldest overall, but stopped
	dead.setState(process.StateStopped)
	procs := map[string]process.Process{"incoming": incoming, "old": old, "dead": dead}

	g := newTestGate(6000, nil, nil)
	g.probe = residentProbe(procs, perModel)

	g.EnsureFits("incoming", procs, nil, gateLog())

	if incoming.stopCalls.Load() != 0 {
		t.Fatalf("incoming model must never be evicted; stops=%d", incoming.stopCalls.Load())
	}
	if dead.stopCalls.Load() != 0 {
		t.Fatalf("already-stopped model must not be stopped again; stops=%d", dead.stopCalls.Load())
	}
	if old.stopCalls.Load() != 1 {
		t.Fatalf("only the resident, non-incoming model should be evicted; old stops=%d", old.stopCalls.Load())
	}
}
