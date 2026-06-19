package scheduler

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

// FairShare, like FIFO, runs entirely on the router's run-loop goroutine, so
// these tests drive its methods directly and synchronously against the same
// fakeEffects/stubPlanner harness used by fifo_test.go.

func newFair(planner Swapper, eff Effects, cfg config.FairShareConfig, limits map[string]int) *FairShare {
	return NewFairShare("test", logmon.NewWriter(io.Discard), planner, cfg, limits, eff)
}

// reqC builds a request for a model from a named caller, stashing the caller on
// the request context the way the middleware does (via shared.ReqContextData.ApiKey).
func reqC(model, caller string) HandlerReq {
	ctx := shared.SetContext(context.Background(), shared.ReqContextData{ApiKey: caller})
	return HandlerReq{Model: model, Ctx: ctx}
}

// servedCallers returns the callers of serve-grants in order, skipping a named
// blocker used to occupy a slot.
func (f *fakeEffects) servedCallers(skip string) []string {
	var out []string
	for _, g := range f.grants {
		if g.err == nil && g.serve && g.caller != skip {
			out = append(out, g.caller)
		}
	}
	return out
}

func TestFairShare_FastPath(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	s := newFair(&stubPlanner{}, eff, config.FairShareConfig{}, map[string]int{"a": 2})

	s.OnRequest(reqC("a", "x"))

	if got := eff.startsFor("a"); got != 0 {
		t.Errorf("StartSwap calls=%d want 0 (fast path)", got)
	}
	if got := eff.served("a"); got != 1 {
		t.Errorf("served(a)=%d want 1", got)
	}
}

func TestFairShare_ModelNotFound(t *testing.T) {
	eff := newFakeEffects()
	s := newFair(&stubPlanner{}, eff, config.FairShareConfig{}, nil)

	s.OnRequest(reqC("ghost", "x"))

	if eff.errored("ghost") != 1 || !errors.Is(eff.grants[0].err, ErrModelNotFound) {
		t.Fatalf("want 1 ErrModelNotFound grant, grants=%+v", eff.grants)
	}
}

// At the concurrency limit a ready model queues the over-limit request rather
// than serving it; a serve-done admits the waiter.
func TestFairShare_ConcurrencyAdmission(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	s := newFair(&stubPlanner{}, eff, config.FairShareConfig{}, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "first")) // fills the only slot
	s.OnRequest(reqC("a", "second"))

	if got := eff.served("a"); got != 1 {
		t.Fatalf("served(a)=%d want 1 (second must queue at limit)", got)
	}

	s.OnServeDone(ServeDoneEvent{ModelID: "a"}) // first finishes -> admit second

	if got := eff.served("a"); got != 2 {
		t.Errorf("served(a)=%d want 2 after slot frees", got)
	}
	if order := eff.servedCallers(""); len(order) != 2 || order[1] != "second" {
		t.Errorf("served order=%v want [first second]", order)
	}
}

// Absolute mode admits the highest-priority waiter first when a slot frees.
func TestFairShare_AbsolutePriorityOrder(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	cfg := config.FairShareConfig{
		Mode:       config.ModeAbsolute,
		Priorities: map[string]int{"hi": 10, "lo": 1},
	}
	s := newFair(&stubPlanner{}, eff, cfg, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "blocker")) // occupies the slot
	s.OnRequest(reqC("a", "lo"))      // queued
	s.OnRequest(reqC("a", "hi"))      // queued (arrived later, higher priority)

	s.OnServeDone(ServeDoneEvent{ModelID: "a"}) // free slot -> admit hi
	s.OnServeDone(ServeDoneEvent{ModelID: "a"}) // -> admit lo

	if order := eff.servedCallers("blocker"); len(order) != 2 || order[0] != "hi" || order[1] != "lo" {
		t.Errorf("served order=%v want [hi lo]", order)
	}
}

// With aging, a long-waiting low-priority request overtakes a freshly-arrived
// higher-priority one.
func TestFairShare_AgingOvertake(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	cfg := config.FairShareConfig{
		Mode:                             config.ModeAbsolute,
		Priorities:                       map[string]int{"hi": 10, "lo": 1},
		PriorityIncreasePerSecondWaiting: 1.0,
	}
	s := newFair(&stubPlanner{}, eff, cfg, map[string]int{"a": 1})

	base := time.Unix(1_000_000, 0)
	clock := base
	s.now = func() time.Time { return clock }

	s.OnRequest(reqC("a", "blocker")) // slot
	s.OnRequest(reqC("a", "lo"))      // enqueued at base
	clock = base.Add(20 * time.Second)
	s.OnRequest(reqC("a", "hi")) // enqueued 20s later

	// At t=base+20s: lo effective = 1 + 20 = 21; hi effective = 10 + 0 = 10.
	s.OnServeDone(ServeDoneEvent{ModelID: "a"})
	s.OnServeDone(ServeDoneEvent{ModelID: "a"})

	if order := eff.servedCallers("blocker"); len(order) != 2 || order[0] != "lo" {
		t.Errorf("served order=%v want lo first (aging overtake)", order)
	}
}

// Proportion mode admits callers in proportion to their weight under sustained
// contention: a weight-3 caller is served markedly more than a weight-1 caller.
func TestFairShare_ProportionShares(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	cfg := config.FairShareConfig{
		Mode:       config.ModeProportion,
		Priorities: map[string]int{"A": 3, "B": 1},
	}
	s := newFair(&stubPlanner{}, eff, cfg, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "blocker")) // occupy slot so nothing fast-paths
	for i := 0; i < 10; i++ {
		s.OnRequest(reqC("a", "A"))
		s.OnRequest(reqC("a", "B"))
	}

	// Drain by repeatedly freeing the slot.
	for i := 0; i < 20; i++ {
		s.OnServeDone(ServeDoneEvent{ModelID: "a"})
	}

	order := eff.servedCallers("blocker")
	if len(order) < 8 {
		t.Fatalf("served only %d, want >= 8: %v", len(order), order)
	}
	a := 0
	for _, c := range order[:8] {
		if c == "A" {
			a++
		}
	}
	if a < 5 {
		t.Errorf("weight-3 caller served %d/8 of first admits, want >=5: %v", a, order[:8])
	}
}

// A full queue sheds the next arrival with a queue_full 429.
func TestFairShare_MaxQueueDepthRejects(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	cfg := config.FairShareConfig{MaxQueueDepth: 1}
	s := newFair(&stubPlanner{}, eff, cfg, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "blocker")) // slot
	s.OnRequest(reqC("a", "q1"))      // queued (depth 1)
	s.OnRequest(reqC("a", "q2"))      // queue full -> 429

	var be *BackpressureError
	if !errors.As(lastErr(eff), &be) || be.Reason != "queue_full" {
		t.Fatalf("want queue_full BackpressureError, got %v", lastErr(eff))
	}
	if be.Concurrency != 1 || be.Waiting != 1 {
		t.Errorf("hint concurrency=%d waiting=%d want 1/1", be.Concurrency, be.Waiting)
	}
}

// A request whose estimated wait exceeds MaxWait is shed at entry.
func TestFairShare_MaxWaitRejects(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	cfg := config.FairShareConfig{MaxWait: 900 * time.Millisecond} // EWMA seed is 1s
	s := newFair(&stubPlanner{}, eff, cfg, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "blocker"))
	s.OnRequest(reqC("a", "x")) // est ~1s > 900ms -> max_wait

	var be *BackpressureError
	if !errors.As(lastErr(eff), &be) || be.Reason != "max_wait" {
		t.Fatalf("want max_wait BackpressureError, got %v", lastErr(eff))
	}
	if be.RetryAfter < 1 {
		t.Errorf("RetryAfter=%d want >=1", be.RetryAfter)
	}
}

// A swap serves a not-ready model, and after it completes the concurrency limit
// still bounds how many waiters are admitted at once.
func TestFairShare_SwapThenLimitedAdmit(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateStopped
	s := newFair(&stubPlanner{}, eff, config.FairShareConfig{}, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "c1")) // starts swap
	s.OnRequest(reqC("a", "c2")) // queued behind swap
	s.OnRequest(reqC("a", "c3")) // queued behind swap

	if got := eff.startsFor("a"); got != 1 {
		t.Fatalf("StartSwap(a)=%d want 1 (one swap for all)", got)
	}

	eff.states["a"] = process.StateReady
	s.OnSwapDone(SwapDone{ModelID: "a"})

	if got := eff.served("a"); got != 1 {
		t.Errorf("served(a)=%d want 1 (limit 1 caps post-swap admits)", got)
	}
	s.OnServeDone(ServeDoneEvent{ModelID: "a"})
	s.OnServeDone(ServeDoneEvent{ModelID: "a"})
	if got := eff.served("a"); got != 3 {
		t.Errorf("served(a)=%d want 3 after draining", got)
	}
}

// A grant the caller did not receive (disconnected) must not strand the slot.
func TestFairShare_GrantFailDoesNotStrandSlot(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	eff.serveResult["a"] = false // every GrantServe reports the caller left
	s := newFair(&stubPlanner{}, eff, config.FairShareConfig{}, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "x")) // grant fails -> inFlight stays 0
	s.OnRequest(reqC("a", "y")) // must still be servable, not blocked

	if got := len(eff.grants); got != 2 {
		t.Errorf("grants=%d want 2 (both attempted, neither stranded)", got)
	}
}

// Queued waiters receive their 1-indexed position on PositionCh.
func TestFairShare_QueuePositionBroadcast(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	cfg := config.FairShareConfig{Priorities: map[string]int{"hi": 10, "lo": 1}}
	s := newFair(&stubPlanner{}, eff, cfg, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "blocker")) // slot

	lo := reqC("a", "lo")
	lo.PositionCh = make(chan int, 1)
	hi := reqC("a", "hi")
	hi.PositionCh = make(chan int, 1)

	s.OnRequest(lo)
	s.OnRequest(hi) // higher priority -> should be position #1

	if pos := drainPos(hi.PositionCh); pos != 1 {
		t.Errorf("hi position=%d want 1", pos)
	}
	if pos := drainPos(lo.PositionCh); pos != 2 {
		t.Errorf("lo position=%d want 2", pos)
	}
}

// OnShutdown errors every queued waiter.
func TestFairShare_ShutdownErrorsWaiters(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	s := newFair(&stubPlanner{}, eff, config.FairShareConfig{}, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "blocker"))
	s.OnRequest(reqC("a", "q1"))
	s.OnRequest(reqC("a", "q2"))

	shutErr := errors.New("shutting down")
	s.OnShutdown(shutErr)

	if got := eff.errored("a"); got != 2 {
		t.Errorf("errored waiters=%d want 2", got)
	}
}

func TestBackpressureError_Response(t *testing.T) {
	be := &BackpressureError{Model: "m", Reason: "queue_full", RetryAfter: 4, Concurrency: 1, Inflight: 1, Waiting: 3}
	if be.StatusCode() != 429 {
		t.Errorf("status=%d want 429", be.StatusCode())
	}
	h := be.Header()
	if h.Get("Retry-After") != "4" || h.Get("X-RateLimit-Limit") != "1" || h.Get("X-RateLimit-Waiting") != "3" {
		t.Errorf("headers=%v", h)
	}
	body := string(be.Body())
	for _, want := range []string{`"reason":"queue_full"`, `"max_concurrency":1`, `"waiting":3`, `"model":"m"`} {
		if !contains(body, want) {
			t.Errorf("body %q missing %q", body, want)
		}
	}
}

func lastErr(f *fakeEffects) error {
	for i := len(f.grants) - 1; i >= 0; i-- {
		if f.grants[i].err != nil {
			return f.grants[i].err
		}
	}
	return nil
}

func drainPos(ch chan int) int {
	last := -1
	for {
		select {
		case p := <-ch:
			last = p
		default:
			return last
		}
	}
}

// TestFairShare_OnCancel_QueuedRequest verifies that cancelling a request
// queued for a concurrency slot prunes it so it never gets admitted.
func TestFairShare_OnCancel_QueuedRequest(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	s := newFair(&stubPlanner{}, eff, config.FairShareConfig{DefaultPriority: 1}, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "blocker")) // fills the only slot
	cancelled := reqC("a", "waiter")
	cancelled.Respond = make(chan HandlerResp, 1)
	s.OnRequest(cancelled) // queued behind the slot
	if len(s.queued) != 1 {
		t.Fatalf("queue len=%d want 1 before cancel", len(s.queued))
	}

	s.OnCancel(cancelled)

	if len(s.queued) != 0 {
		t.Fatalf("queue len=%d want 0 after cancel", len(s.queued))
	}

	// Slot frees; the cancelled waiter must not be served.
	s.OnServeDone(ServeDoneEvent{ModelID: "a"})
	if got := eff.served("a"); got != 1 {
		t.Errorf("served(a)=%d want 1 (only the blocker; cancelled waiter pruned)", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Ungated (non-inference) requests bypass the concurrency limit: at a saturated
// model they are served untracked (no slot consumed) instead of queueing, so the
// model's web UI stays reachable. The queued inference request is unaffected.
func TestFairShare_UngatedBypassesConcurrency(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	cfg := config.FairShareConfig{GatedPaths: `^/v1/`}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	s := newFair(&stubPlanner{}, eff, cfg, map[string]int{"a": 1})

	gated := func(caller string) HandlerReq {
		r := reqC("a", caller)
		r.Path = "/v1/chat/completions"
		return r
	}

	s.OnRequest(gated("first"))  // fills the only slot
	s.OnRequest(gated("queued")) // over limit -> queued
	if got := eff.trackedServed("a"); got != 1 {
		t.Fatalf("trackedServed(a)=%d want 1 (second gated request must queue)", got)
	}

	// UI request arrives while the model is saturated.
	ui := reqC("a", "ui")
	ui.Path = "/"
	s.OnRequest(ui)

	if got := eff.untrackedServed("a"); got != 1 {
		t.Fatalf("untrackedServed(a)=%d want 1 (UI request served without a slot)", got)
	}
	if got := eff.trackedServed("a"); got != 1 {
		t.Errorf("trackedServed(a)=%d want 1 (UI must not consume a slot or admit the waiter)", got)
	}

	s.OnServeDone(ServeDoneEvent{ModelID: "a"}) // first finishes -> admit queued
	if got := eff.trackedServed("a"); got != 2 {
		t.Errorf("trackedServed(a)=%d want 2 after slot frees", got)
	}
}

// Interactive requests form a hard tier: an interactive waiter is admitted ahead
// of a non-interactive one even when the batch request has far higher priority.
func TestFairShare_InteractiveTier(t *testing.T) {
	eff := newFakeEffects()
	eff.states["a"] = process.StateReady
	cfg := config.FairShareConfig{
		Mode:       config.ModeAbsolute,
		Priorities: map[string]int{"batch": 100, "ui": 1},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	s := newFair(&stubPlanner{}, eff, cfg, map[string]int{"a": 1})

	s.OnRequest(reqC("a", "blocker")) // occupies the only slot

	batch := reqC("a", "batch") // priority 100, not interactive
	s.OnRequest(batch)

	ui := reqC("a", "ui") // priority 1, interactive
	ui.Interactive = true
	s.OnRequest(ui)

	s.OnServeDone(ServeDoneEvent{ModelID: "a"}) // admit interactive 'ui' first...
	s.OnServeDone(ServeDoneEvent{ModelID: "a"}) // ...then high-priority batch

	order := eff.servedCallers("blocker")
	if len(order) != 2 || order[0] != "ui" || order[1] != "batch" {
		t.Errorf("served order=%v want [ui batch] (interactive tier beats higher priority)", order)
	}
}
