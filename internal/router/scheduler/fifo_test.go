package scheduler

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// FIFO methods all run on the router's single run-loop goroutine, so these
// tests drive them directly and synchronously. A swap is "completed" by calling
// OnSwapDone, a served request "finishes" by calling OnServeDone — exactly the
// events the run loop would deliver. fakeEffects records every side-effect and
// stubPlanner supplies a fixed eviction set per target.

// stubPlanner returns a fixed eviction list per target.
type stubPlanner struct {
	evict map[string][]string
}

func (s *stubPlanner) EvictionFor(target string, _ []string) []string {
	if s.evict == nil {
		return nil
	}
	return s.evict[target]
}

func (s *stubPlanner) OnSwapStart(string, []string) {}

// grantRec is one GrantError / GrantServe call. err!=nil marks an error grant;
// otherwise it is a serve grant and serve reports whether the caller received it.
type grantRec struct {
	model string
	err   error
	serve bool
}

type startRec struct {
	model string
	evict []string
}

type stopRec struct {
	timeout time.Duration
	ids     []string
}

// fakeEffects is an in-memory scheduler.Effects. Tests program process states
// and GrantServe outcomes, then assert on the recorded calls.
type fakeEffects struct {
	states      map[string]process.ProcessState // model -> state; missing => not handled
	serveResult map[string]bool                 // GrantServe return per model (default true)

	starts []startRec
	grants []grantRec
	stops  []stopRec
}

func newFakeEffects() *fakeEffects {
	return &fakeEffects{
		states:      map[string]process.ProcessState{},
		serveResult: map[string]bool{},
	}
}

func (f *fakeEffects) ModelState(modelID string) (process.ProcessState, bool) {
	st, ok := f.states[modelID]
	return st, ok
}

func (f *fakeEffects) RunningModels() map[string]process.ProcessState {
	out := make(map[string]process.ProcessState)
	for id, st := range f.states {
		if st == process.StateStopped || st == process.StateShutdown {
			continue
		}
		out[id] = st
	}
	return out
}

func (f *fakeEffects) StartSwap(modelID string, evict []string) {
	f.starts = append(f.starts, startRec{model: modelID, evict: evict})
}

func (f *fakeEffects) GrantError(req HandlerReq, err error) {
	f.grants = append(f.grants, grantRec{model: req.Model, err: err})
}

func (f *fakeEffects) GrantServe(req HandlerReq, modelID string) bool {
	ok := true
	if v, set := f.serveResult[modelID]; set {
		ok = v
	}
	f.grants = append(f.grants, grantRec{model: modelID, serve: ok})
	return ok
}

func (f *fakeEffects) StopProcesses(timeout time.Duration, ids []string) {
	f.stops = append(f.stops, stopRec{timeout: timeout, ids: ids})
}

// served counts grants that handed modelID a handler and were received.
func (f *fakeEffects) served(modelID string) int {
	n := 0
	for _, g := range f.grants {
		if g.err == nil && g.serve && g.model == modelID {
			n++
		}
	}
	return n
}

// errored counts error grants, optionally filtered by model ("" = any).
func (f *fakeEffects) errored(model string) int {
	n := 0
	for _, g := range f.grants {
		if g.err != nil && (model == "" || g.model == model) {
			n++
		}
	}
	return n
}

// startsFor counts StartSwap calls for modelID.
func (f *fakeEffects) startsFor(modelID string) int {
	n := 0
	for _, s := range f.starts {
		if s.model == modelID {
			n++
		}
	}
	return n
}

func newFIFO(planner Swapper, eff Effects) *FIFO {
	return NewFIFO("test", logmon.NewWriter(io.Discard), planner, eff)
}

func req(model string) HandlerReq { return HandlerReq{Model: model} }

func TestFIFO_FastPath(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	s := newFIFO(&stubPlanner{}, eff)

	s.OnRequest(req("a"))

	if got := eff.startsFor("a"); got != 0 {
		t.Errorf("StartSwap calls=%d want 0 (fast path should not swap)", got)
	}
	if got := eff.served("a"); got != 1 {
		t.Errorf("served(a)=%d want 1", got)
	}
}

func TestFIFO_ModelNotFound(t *testing.T) {
	eff := newFakeEffects() // no states => model unknown
	s := newFIFO(&stubPlanner{}, eff)

	s.OnRequest(req("ghost"))

	if got := len(eff.starts); got != 0 {
		t.Errorf("StartSwap calls=%d want 0", got)
	}
	if eff.errored("ghost") != 1 {
		t.Fatalf("want 1 error grant for ghost, grants=%+v", eff.grants)
	}
	if !errors.Is(eff.grants[0].err, ErrModelNotFound) {
		t.Errorf("err=%v want ErrModelNotFound", eff.grants[0].err)
	}
}

func TestFIFO_OnDemandStartThenServe(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	s := newFIFO(&stubPlanner{}, eff)

	s.OnRequest(req("a"))
	if got := eff.startsFor("a"); got != 1 {
		t.Fatalf("StartSwap(a)=%d want 1", got)
	}
	if got := eff.served("a"); got != 0 {
		t.Errorf("served(a)=%d want 0 before swap completes", got)
	}

	// Swap finishes, model is now ready.
	eff.states["a"] = process.StateReady
	s.OnSwapDone(SwapDone{ModelID: "a"})

	if got := eff.served("a"); got != 1 {
		t.Errorf("served(a)=%d want 1 after swap done", got)
	}
}

func TestFIFO_JoinInFlightSwap(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	s := newFIFO(&stubPlanner{}, eff)

	s.OnRequest(req("a")) // starts swap
	s.OnRequest(req("a")) // joins
	s.OnRequest(req("a")) // joins

	if got := eff.startsFor("a"); got != 1 {
		t.Fatalf("StartSwap(a)=%d want 1 (all three share one swap)", got)
	}

	eff.states["a"] = process.StateReady
	s.OnSwapDone(SwapDone{ModelID: "a"})

	if got := eff.served("a"); got != 3 {
		t.Errorf("served(a)=%d want 3 (one swap serves all waiters)", got)
	}
}

func TestFIFO_SwapDoneError_FailsAllWaiters(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	s := newFIFO(&stubPlanner{}, eff)

	s.OnRequest(req("a"))
	s.OnRequest(req("a"))

	s.OnSwapDone(SwapDone{ModelID: "a", Err: errors.New("boom")})

	if eff.served("a") != 0 {
		t.Errorf("served(a)=%d want 0 on swap error", eff.served("a"))
	}
	if eff.errored("a") != 2 {
		t.Errorf("errored(a)=%d want 2 (both waiters fail)", eff.errored("a"))
	}
}

// TestFIFO_QueueOnEvictionCollision covers a request whose target evicts the
// model currently being swapped: it must queue until that swap finishes AND its
// served request drains, because starting it would stop a busy process.
func TestFIFO_QueueOnEvictionCollision(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	eff.states["b"] = process.StateStopped
	// Loading b evicts a.
	s := newFIFO(&stubPlanner{evict: map[string][]string{"b": {"a"}}}, eff)

	s.OnRequest(req("a")) // StartSwap(a)
	s.OnRequest(req("b")) // collides with a's in-flight swap -> queue
	if got := eff.startsFor("b"); got != 0 {
		t.Fatalf("b started early: StartSwap(b)=%d want 0", got)
	}

	// a becomes ready and is granted (now serving, inFlight[a]=1).
	eff.states["a"] = process.StateReady
	s.OnSwapDone(SwapDone{ModelID: "a"})
	if got := eff.startsFor("b"); got != 0 {
		t.Fatalf("b started while a is serving: StartSwap(b)=%d want 0", got)
	}

	// a's request finishes -> a no longer in-flight -> b may now swap.
	s.OnServeDone(ServeDoneEvent{ModelID: "a"})
	if got := eff.startsFor("b"); got != 1 {
		t.Fatalf("StartSwap(b)=%d want 1 after a drained", got)
	}
	if got := eff.starts[len(eff.starts)-1].evict; len(got) != 1 || got[0] != "a" {
		t.Errorf("b swap evict=%v want [a]", got)
	}
}

// TestFIFO_DisjointSwapsRunInParallel verifies two requests with
// non-conflicting evict sets both start without waiting for each other.
func TestFIFO_DisjointSwapsRunInParallel(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	eff.states["b"] = process.StateStopped
	s := newFIFO(&stubPlanner{}, eff) // empty evicts

	s.OnRequest(req("a"))
	s.OnRequest(req("b"))

	if eff.startsFor("a") != 1 || eff.startsFor("b") != 1 {
		t.Fatalf("StartSwap a=%d b=%d want 1 each (parallel)", eff.startsFor("a"), eff.startsFor("b"))
	}
}

// TestFIFO_QueueDrainPromotesMultiple verifies completing one swap unblocks
// every queued request that no longer collides — they all start together.
func TestFIFO_QueueDrainPromotesMultiple(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	eff.states["b"] = process.StateStopped
	eff.states["c"] = process.StateStopped
	// a's swap evicts both b and c; b and c evict nothing.
	s := newFIFO(&stubPlanner{evict: map[string][]string{"a": {"b", "c"}}}, eff)

	s.OnRequest(req("a")) // StartSwap(a, [b,c])
	s.OnRequest(req("b")) // collides (in a's evict set) -> queue
	s.OnRequest(req("c")) // collides -> queue
	if eff.startsFor("b") != 0 || eff.startsFor("c") != 0 {
		t.Fatalf("b/c started early")
	}

	eff.states["a"] = process.StateReady
	s.OnSwapDone(SwapDone{ModelID: "a"})

	// b and c have empty evict sets and don't evict a, so both start now.
	if eff.startsFor("b") != 1 || eff.startsFor("c") != 1 {
		t.Fatalf("StartSwap b=%d c=%d want 1 each after a done", eff.startsFor("b"), eff.startsFor("c"))
	}
	if eff.served("a") != 1 {
		t.Errorf("served(a)=%d want 1", eff.served("a"))
	}
}

// TestFIFO_QueueCollation verifies duplicate requests collapse into one swap
// per model: the second request for each model joins the active swap (at arrival
// or at drain time) rather than triggering its own swap.
func TestFIFO_QueueCollation(t *testing.T) {
	eff := newFakeEffects()
	for _, id := range []string{"a", "b", "c"} {
		eff.states[id] = process.StateStopped
	}
	// Each model evicts the other two: all swaps are mutually exclusive.
	s := newFIFO(&stubPlanner{evict: map[string][]string{
		"a": {"b", "c"},
		"b": {"a", "c"},
		"c": {"a", "b"},
	}}, eff)

	for _, id := range []string{"a", "b", "c", "a", "b", "c"} {
		s.OnRequest(req(id))
	}

	// Drain a, then its served requests, which promotes b; repeat for b -> c.
	drain := func(model string, waiters int) {
		eff.states[model] = process.StateReady
		s.OnSwapDone(SwapDone{ModelID: model})
		for i := 0; i < waiters; i++ {
			s.OnServeDone(ServeDoneEvent{ModelID: model})
		}
	}
	drain("a", 2)
	drain("b", 2)
	drain("c", 2)

	for _, id := range []string{"a", "b", "c"} {
		if got := eff.startsFor(id); got != 1 {
			t.Errorf("StartSwap(%s)=%d want 1 (collation)", id, got)
		}
		if got := eff.served(id); got != 2 {
			t.Errorf("served(%s)=%d want 2", id, got)
		}
	}
}

// TestFIFO_NoSwapWhileServing verifies a model still handling requests is not
// evicted: the evicting request waits until every in-flight request drains.
func TestFIFO_NoSwapWhileServing(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	eff.states["b"] = process.StateStopped
	s := newFIFO(&stubPlanner{evict: map[string][]string{"b": {"a"}}}, eff)

	s.OnRequest(req("a")) // fast path, inFlight[a]=1
	s.OnRequest(req("a")) // fast path, inFlight[a]=2
	s.OnRequest(req("b")) // would evict busy a -> queue
	if eff.startsFor("b") != 0 {
		t.Fatalf("b started while a serving")
	}

	s.OnServeDone(ServeDoneEvent{ModelID: "a"}) // inFlight[a]=1
	if eff.startsFor("b") != 0 {
		t.Fatalf("b started while a still serving one request")
	}

	s.OnServeDone(ServeDoneEvent{ModelID: "a"}) // inFlight[a]=0
	if eff.startsFor("b") != 1 {
		t.Fatalf("StartSwap(b)=%d want 1 after a fully drained", eff.startsFor("b"))
	}
}

// TestFIFO_GrantServeFalseDoesNotLeakInFlight verifies that when a caller has
// walked away (GrantServe returns false) the in-flight count is not bumped, so a
// later evicting request is not blocked forever.
func TestFIFO_GrantServeFalseDoesNotLeakInFlight(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	eff.states["b"] = process.StateStopped
	eff.serveResult["a"] = false // a's waiter is gone by grant time
	s := newFIFO(&stubPlanner{evict: map[string][]string{"b": {"a"}}}, eff)

	s.OnRequest(req("a"))
	eff.states["a"] = process.StateReady
	s.OnSwapDone(SwapDone{ModelID: "a"}) // grant fails, inFlight[a] stays 0

	// b evicts a; since a is not in-flight, b should start immediately.
	s.OnRequest(req("b"))
	if eff.startsFor("b") != 1 {
		t.Fatalf("StartSwap(b)=%d want 1 (no leaked in-flight on a)", eff.startsFor("b"))
	}
}

// TestFIFO_OnShutdown_FailsAllWaiters verifies shutdown errors every waiter the
// scheduler holds: active-swap waiters and queued requests alike.
func TestFIFO_OnShutdown_FailsAllWaiters(t *testing.T) {
	eff := newFakeEffects()
	for _, id := range []string{"a", "b", "c"} {
		eff.states[id] = process.StateStopped
	}
	// a and b load in parallel; c collides with both and queues.
	s := newFIFO(&stubPlanner{evict: map[string][]string{"c": {"a", "b"}}}, eff)

	s.OnRequest(req("a")) // StartSwap(a)
	s.OnRequest(req("a")) // join a
	s.OnRequest(req("b")) // StartSwap(b)
	s.OnRequest(req("b")) // join b
	s.OnRequest(req("c")) // queued

	s.OnShutdown(errors.New("shutting down"))

	if got := eff.errored(""); got != 5 {
		t.Errorf("error grants=%d want 5 (2 a + 2 b + 1 c)", got)
	}
}

func TestFIFO_OnUnload_ReleasesActiveWaiters(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	s := newFIFO(&stubPlanner{}, eff)

	s.OnRequest(req("a")) // active swap a with one waiter
	s.OnRequest(req("a")) // join

	s.OnUnload([]string{"a"}, time.Second)

	if got := eff.errored("a"); got != 2 {
		t.Errorf("errored(a)=%d want 2 (active swap waiters released)", got)
	}
	if len(eff.stops) != 1 || len(eff.stops[0].ids) != 1 || eff.stops[0].ids[0] != "a" {
		t.Errorf("StopProcesses=%+v want one call stopping [a]", eff.stops)
	}
	if eff.stops[0].timeout != time.Second {
		t.Errorf("StopProcesses timeout=%v want 1s", eff.stops[0].timeout)
	}
}

func TestFIFO_OnUnload_DropsQueuedRequests(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	eff.states["b"] = process.StateStopped
	// b evicts a, so a request for b queues while a is loading.
	s := newFIFO(&stubPlanner{evict: map[string][]string{"b": {"a"}}}, eff)

	s.OnRequest(req("a")) // StartSwap(a)
	s.OnRequest(req("b")) // queued

	s.OnUnload([]string{"b"}, time.Second)

	if got := eff.errored("b"); got != 1 {
		t.Errorf("errored(b)=%d want 1 (queued request dropped)", got)
	}
	if got := eff.startsFor("b"); got != 0 {
		t.Errorf("StartSwap(b)=%d want 0 (b should never start)", got)
	}
	// a's swap is untouched: its waiter is neither served nor errored yet.
	if eff.served("a") != 0 || eff.errored("a") != 0 {
		t.Errorf("a swap should be untouched: served=%d errored=%d", eff.served("a"), eff.errored("a"))
	}
}
