package scheduler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/shared"
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
	states       map[string]process.ProcessState // model -> state; missing => not handled
	serveResult  map[string]bool                 // GrantServe return per model (default true)
	lastServeReq HandlerReq

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
	f.lastServeReq = req
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
	return NewFIFO("test", logmon.NewWriter(io.Discard), planner, config.FifoConfig{}, nil, eff)
}

func req(model string) HandlerReq { return HandlerReq{Model: model} }

// reqCh creates a HandlerReq with a unique Respond channel so OnCancel can
// identify it among queued requests and swap waiters.
func reqCh(model string) HandlerReq {
	return HandlerReq{
		Model:   model,
		Respond: make(chan HandlerResp, 1),
	}
}

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

func TestFIFO_GrantSetsPriorityMetadata(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	cfg := config.FifoConfig{Priority: map[string]int{"a": 7}}
	s := NewFIFO("test", logmon.NewWriter(io.Discard), &stubPlanner{}, cfg, nil, eff)

	ctx := shared.SetContext(context.Background(), shared.ReqContextData{ModelID: "a", Metadata: make(map[string]string)})
	s.OnRequest(HandlerReq{Model: "a", Ctx: ctx})

	if got := eff.served("a"); got != 1 {
		t.Fatalf("served(a)=%d want 1", got)
	}
	data, ok := shared.ReadContext(eff.lastServeReq.Ctx)
	if !ok {
		t.Fatal("context data missing from granted request")
	}
	if data.Metadata["fifo_priority"] != "7" {
		t.Errorf("fifo_priority = %q, want 7", data.Metadata["fifo_priority"])
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

// TestFIFO_OverlappingEvictSetsDoNotRunInParallel verifies two swaps with
// different targets that evict the *same* model do not run concurrently: the
// second must queue rather than double-evict the shared model. Neither target is
// in the other's evict set, so this is only caught by the evict-set overlap
// check in collidesWith.
func TestFIFO_OverlappingEvictSetsDoNotRunInParallel(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	eff.states["b"] = process.StateStopped
	eff.states["x"] = process.StateReady // shared eviction target, running
	// Loading a or b both require evicting x.
	s := newFIFO(&stubPlanner{evict: map[string][]string{"a": {"x"}, "b": {"x"}}}, eff)

	s.OnRequest(req("a")) // StartSwap(a, [x])
	s.OnRequest(req("b")) // overlaps a's evict set ([x]) -> queue
	if eff.startsFor("a") != 1 {
		t.Fatalf("StartSwap(a)=%d want 1", eff.startsFor("a"))
	}
	if got := eff.startsFor("b"); got != 0 {
		t.Fatalf("b started in parallel while a evicts x: StartSwap(b)=%d want 0", got)
	}

	// a's swap completes and x is gone; b can now evict nothing and start.
	eff.states["a"] = process.StateReady
	eff.states["x"] = process.StateStopped
	s.OnSwapDone(SwapDone{ModelID: "a"})
	if got := eff.startsFor("b"); got != 1 {
		t.Fatalf("StartSwap(b)=%d want 1 after a's swap drained", got)
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

// TestFIFO_PriorityQueueOrder verifies queued requests are ordered by descending
// priority, with arrival (FIFO) order preserved among equal-priority models.
func TestFIFO_PriorityQueueOrder(t *testing.T) {
	eff := newFakeEffects()
	for _, m := range []string{"z", "A", "B", "C", "D"} {
		eff.states[m] = process.StateStopped
	}
	// z's swap evicts every other model, so any request that arrives while z is
	// loading collides with z's in-flight swap and parks in the queue.
	planner := &stubPlanner{evict: map[string][]string{"z": {"A", "B", "C", "D"}}}
	cfg := config.FifoConfig{Priority: map[string]int{"A": 10, "B": 5, "C": 5, "D": 1}}
	s := NewFIFO("test", logmon.NewWriter(io.Discard), planner, cfg, nil, eff)

	s.OnRequest(req("z")) // StartSwap(z, [A,B,C,D])

	// Arrive out of priority order; B before C exercises FIFO tie-breaking.
	for _, m := range []string{"B", "D", "C", "A"} {
		s.OnRequest(req(m))
	}

	got := make([]string, len(s.queued))
	for i, q := range s.queued {
		got[i] = q.Model
	}
	want := []string{"A", "B", "C", "D"}
	if len(got) != len(want) {
		t.Fatalf("queue=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("queue=%v want %v", got, want)
		}
	}
}

// TestFIFO_OnCancel_QueuedRequest verifies that cancelling a queued request
// prevents drainQueue from ever starting a model load for it. Without OnCancel
// the dead request would sit in the queue until a drain triggers a wasted swap.
func TestFIFO_OnCancel_QueuedRequest(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	eff.states["b"] = process.StateStopped
	// b evicts a, so a request for b queues while a is loading.
	s := newFIFO(&stubPlanner{evict: map[string][]string{"b": {"a"}}}, eff)

	s.OnRequest(req("a")) // StartSwap(a)

	cancelledReq := reqCh("b")
	s.OnRequest(cancelledReq) // queued (collides with a's in-flight swap)
	if len(s.queued) != 1 {
		t.Fatalf("queue len=%d want 1 before cancel", len(s.queued))
	}

	// Client disconnects.
	s.OnCancel(cancelledReq)

	if len(s.queued) != 0 {
		t.Fatalf("queue len=%d want 0 after cancel", len(s.queued))
	}

	// a's swap finishes; drainQueue runs but b is gone — no swap for b.
	eff.states["a"] = process.StateReady
	s.OnSwapDone(SwapDone{ModelID: "a"})

	if got := eff.startsFor("b"); got != 0 {
		t.Errorf("StartSwap(b)=%d want 0 (cancelled request should not trigger a load)", got)
	}
}

// TestFIFO_OnCancel_SwapWaiter verifies that cancelling a request that joined an
// in-flight swap removes it from the waiter list. When the swap completes, the
// cancelled waiter receives no grant and does not bump the in-flight count.
func TestFIFO_OnCancel_SwapWaiter(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	s := newFIFO(&stubPlanner{}, eff)

	liveReq := reqCh("a")
	cancelledReq := reqCh("a")
	s.OnRequest(liveReq)      // starts swap
	s.OnRequest(cancelledReq) // joins

	if sw := s.active["a"]; len(sw.waiters) != 2 {
		t.Fatalf("waiters=%d want 2", len(sw.waiters))
	}

	s.OnCancel(cancelledReq)

	if sw := s.active["a"]; len(sw.waiters) != 1 {
		t.Fatalf("waiters=%d want 1 after cancel", len(sw.waiters))
	}

	// Swap finishes: only the live waiter is granted.
	eff.states["a"] = process.StateReady
	s.OnSwapDone(SwapDone{ModelID: "a"})

	if got := eff.served("a"); got != 1 {
		t.Errorf("served(a)=%d want 1 (only the non-cancelled waiter)", got)
	}
}

// TestFIFO_OnCancel_NotPresent is a no-op: cancelling a request that was already
// granted (and is no longer queued or waiting) must not affect anything.
func TestFIFO_OnCancel_NotPresent(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	s := newFIFO(&stubPlanner{}, eff)

	r := reqCh("a")
	s.OnRequest(r) // fast-path served immediately

	// Cancel after grant — should be a harmless no-op.
	s.OnCancel(r)

	if got := eff.served("a"); got != 1 {
		t.Errorf("served(a)=%d want 1 (cancel of granted request is a no-op)", got)
	}
	if len(s.queued) != 0 {
		t.Errorf("queue should be empty, len=%d", len(s.queued))
	}
}

// newFIFOWithLimit builds a FIFO whose single model has the given concurrency
// limit, already in StateReady so every request exercises the fast path.
func newFIFOWithLimit(t *testing.T, model string, limit int) (*FIFO, *fakeEffects) {
	t.Helper()
	eff := newFakeEffects()
	eff.states[model] = process.StateReady
	models := map[string]config.ModelConfig{
		model: {ConcurrencyLimit: limit},
	}
	s := NewFIFO("test", logmon.NewWriter(io.Discard), &stubPlanner{}, config.FifoConfig{}, models, eff)
	return s, eff
}

// TestFIFO_ConcurrencyLimit_RejectsOverLimit verifies that a request arriving
// while the model is at capacity gets an error grant instead of being served,
// and that a new request succeeds once an in-flight one completes.
func TestFIFO_ConcurrencyLimit_RejectsOverLimit(t *testing.T) {
	s, eff := newFIFOWithLimit(t, "a", 1)

	// First request: served (inFlight 0 → 1).
	s.OnRequest(req("a"))
	if got := eff.served("a"); got != 1 {
		t.Fatalf("served(a)=%d want 1", got)
	}

	// Second request while slot is occupied: rejected with HTTPError 429.
	s.OnRequest(req("a"))
	if got := eff.errored("a"); got != 1 {
		t.Fatalf("errored(a)=%d want 1 (over-limit)", got)
	}
	var httpErr shared.HTTPError
	if !errors.As(eff.grants[len(eff.grants)-1].err, &httpErr) {
		t.Fatalf("err=%v want HTTPError", eff.grants[len(eff.grants)-1].err)
	}
	if httpErr.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("StatusCode()=%d want 429", httpErr.StatusCode())
	}
	if httpErr.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}

	// After the in-flight request finishes, a new request succeeds.
	s.OnServeDone(ServeDoneEvent{ModelID: "a"})
	s.OnRequest(req("a"))
	if got := eff.served("a"); got != 2 {
		t.Fatalf("served(a)=%d want 2 after drain", got)
	}
}

// TestFIFO_ConcurrencyLimit_DefaultIsTen verifies that a model without an
// explicit ConcurrencyLimit gets the default cap of 10.
func TestFIFO_ConcurrencyLimit_DefaultIsTen(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	// nil models → every model gets defaultConcurrencyLimit (10).
	s := newFIFO(&stubPlanner{}, eff)

	for i := 0; i < 10; i++ {
		s.OnRequest(req("a"))
	}
	if got := eff.served("a"); got != 10 {
		t.Fatalf("served(a)=%d want 10 (default limit)", got)
	}

	// 11th request is rejected.
	s.OnRequest(req("a"))
	if got := eff.errored("a"); got != 1 {
		t.Fatalf("errored(a)=%d want 1 (over default limit)", got)
	}
}

// TestFIFO_ConcurrencyLimit_CustomLimit verifies a ConcurrencyLimit greater
// than zero overrides the default.
func TestFIFO_ConcurrencyLimit_CustomLimit(t *testing.T) {
	s, eff := newFIFOWithLimit(t, "a", 2)

	s.OnRequest(req("a"))
	s.OnRequest(req("a"))
	s.OnRequest(req("a"))

	if got := eff.served("a"); got != 2 {
		t.Fatalf("served(a)=%d want 2 (custom limit)", got)
	}
	if got := eff.errored("a"); got != 1 {
		t.Fatalf("errored(a)=%d want 1 (over custom limit)", got)
	}
}

// TestFIFO_ConcurrencyLimit_SwapWaiters verifies that when more swap waiters
// exist than the concurrency limit, excess waiters are rejected on swap
// completion rather than exceeding the limit.
func TestFIFO_ConcurrencyLimit_SwapWaiters(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	models := map[string]config.ModelConfig{
		"a": {ConcurrencyLimit: 2},
	}
	s := NewFIFO("test", logmon.NewWriter(io.Discard), &stubPlanner{}, config.FifoConfig{}, models, eff)

	// Three requests arrive while model is loading: one starts swap, two join.
	s.OnRequest(req("a"))
	s.OnRequest(req("a"))
	s.OnRequest(req("a"))

	if got := eff.startsFor("a"); got != 1 {
		t.Fatalf("StartSwap(a)=%d want 1", got)
	}

	// Swap completes: two served (limit), one rejected.
	eff.states["a"] = process.StateReady
	s.OnSwapDone(SwapDone{ModelID: "a"})

	if got := eff.served("a"); got != 2 {
		t.Fatalf("served(a)=%d want 2 (limit on swap completion)", got)
	}
	if got := eff.errored("a"); got != 1 {
		t.Fatalf("errored(a)=%d want 1 (excess waiter rejected)", got)
	}
}
