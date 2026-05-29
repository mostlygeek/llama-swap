package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// stubPlanner is a swapPlanner that returns a fixed eviction list per target
// and never logs. It lets the base-router tests cover shared run-loop
// behaviour without dragging in either real router's eviction rules.
type stubPlanner struct {
	evict map[string][]string
}

func (s *stubPlanner) EvictionFor(target string, _ []string) []string {
	if s.evict == nil {
		return nil
	}
	return s.evict[target]
}

func (s *stubPlanner) OnSwapStart(string) {}

func newTestBase(t *testing.T, processes map[string]process.Process, planner swapPlanner) *baseRouter {
	t.Helper()
	conf := config.Config{HealthCheckTimeout: 5}
	b := newBaseRouter("test", conf, processes, planner, logmon.NewWriter(io.Discard))
	b.testProcessed = make(chan struct{}, 64)
	go b.run()
	t.Cleanup(func() {
		if !b.shuttingDown.Load() {
			_ = b.Shutdown(time.Second)
		}
	})
	return b
}

func TestBaseRouter_RunningModels(t *testing.T) {
	ready := newFakeProcess("ready")
	ready.markReady()
	starting := newFakeProcess("starting")
	starting.setState(process.StateStarting)
	stopped := newFakeProcess("stopped")

	b := newTestBase(t, map[string]process.Process{
		"ready": ready, "starting": starting, "stopped": stopped,
	}, &stubPlanner{})

	running := b.RunningModels()
	if len(running) != 2 {
		t.Fatalf("running=%v want 2 entries", running)
	}
	if running["ready"] != process.StateReady {
		t.Errorf("ready state=%q want ready", running["ready"])
	}
	if running["starting"] != process.StateStarting {
		t.Errorf("starting state=%q want starting", running["starting"])
	}
	if _, ok := running["stopped"]; ok {
		t.Errorf("stopped process should be excluded from RunningModels")
	}
}

func TestBaseRouter_UnloadAll(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	c := newFakeProcess("c")
	c.markReady()

	b := newTestBase(t, map[string]process.Process{"a": a, "c": c}, &stubPlanner{})
	b.Unload(time.Second)

	if a.State() != process.StateStopped || c.State() != process.StateStopped {
		t.Fatalf("Unload() should stop every process: a=%q c=%q", a.State(), c.State())
	}
}

func TestBaseRouter_UnloadSpecificModel(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	c := newFakeProcess("c")
	c.markReady()

	b := newTestBase(t, map[string]process.Process{"a": a, "c": c}, &stubPlanner{})
	b.Unload(time.Second, "a")

	if a.State() != process.StateStopped {
		t.Errorf("a should be stopped, got %q", a.State())
	}
	if c.State() != process.StateReady {
		t.Errorf("c should remain ready, got %q", c.State())
	}
}

// TestBaseRouter_Unload_StopsInParallel verifies that Unload fans out its
// Stop calls concurrently rather than stopping each process serially. Each
// fakeProcess.Stop is pinned via stopBlock; the test only releases them
// after observing every stopStarted, proving all three Stops were in
// flight simultaneously.
func TestBaseRouter_Unload_StopsInParallel(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	a.stopBlock = make(chan struct{})
	pb := newFakeProcess("b")
	pb.markReady()
	pb.stopBlock = make(chan struct{})
	pc := newFakeProcess("c")
	pc.markReady()
	pc.stopBlock = make(chan struct{})

	b := newTestBase(t, map[string]process.Process{"a": a, "b": pb, "c": pc}, &stubPlanner{})

	unloadDone := make(chan struct{})
	go func() {
		b.Unload(time.Second, "a", "b", "c")
		close(unloadDone)
	}()

	// All three Stop calls must start before any of them are allowed to
	// complete. If Unload was serial, only one stopStarted would fire
	// until we released its stopBlock, and this would deadlock.
	for _, p := range []*fakeProcess{a, pb, pc} {
		select {
		case <-p.stopStarted:
		case <-time.After(2 * time.Second):
			t.Fatalf("Stop on %s never started — Unload is not parallel", p.id)
		}
	}

	// Release them; Unload should now return.
	close(a.stopBlock)
	close(pb.stopBlock)
	close(pc.stopBlock)

	select {
	case <-unloadDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Unload did not return after stops released")
	}

	for _, p := range []*fakeProcess{a, pb, pc} {
		if p.State() != process.StateStopped {
			t.Errorf("%s state=%q want stopped", p.id, p.State())
		}
		if got := p.stopCalls.Load(); got != 1 {
			t.Errorf("%s stopCalls=%d want 1", p.id, got)
		}
	}
}

// TestBaseRouter_Unload_ReleasesActiveSwapWaiters verifies that Unload
// rejoins router state: a request whose swap to the unloaded model is
// still in progress receives an error, instead of being abandoned
// against a process that's about to vanish.
func TestBaseRouter_Unload_ReleasesActiveSwapWaiters(t *testing.T) {
	a := newFakeProcess("a")
	// autoReady=false: the swap parks on WaitReady so we can interrupt
	// it with Unload before it completes.

	b := newTestBase(t, map[string]process.Process{"a": a}, &stubPlanner{})

	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		b.ServeHTTP(w, newRequest("a"))
		close(done)
	}()
	waitProcessed(t, b.testProcessed, 1) // handlerReq absorbed; swap started
	<-a.runStarted

	b.Unload(time.Second, "a")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeHTTP did not return after Unload")
	}
	if w.Code == http.StatusOK {
		t.Errorf("expected non-OK status after Unload, got %d body=%q", w.Code, w.Body.String())
	}
	if a.State() != process.StateStopped {
		t.Errorf("a state=%q want stopped", a.State())
	}
}

// TestBaseRouter_Unload_DropsQueuedRequests verifies that queued requests
// for an unloaded model receive an error rather than sitting forever in
// the queue against state the router no longer maintains.
func TestBaseRouter_Unload_DropsQueuedRequests(t *testing.T) {
	a := newFakeProcess("a")
	pb := newFakeProcess("b")
	// Loading B evicts A — so a request for B while A is loading queues.
	planner := &stubPlanner{evict: map[string][]string{"b": {"a"}}}
	b := newTestBase(t, map[string]process.Process{"a": a, "b": pb}, planner)

	// r1 starts the swap to A and parks on WaitReady (autoReady=false).
	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		b.ServeHTTP(w1, newRequest("a"))
		close(done1)
	}()
	waitProcessed(t, b.testProcessed, 1)
	<-a.runStarted

	// r2 for B collides with A's in-flight swap and queues.
	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		b.ServeHTTP(w2, newRequest("b"))
		close(done2)
	}()
	waitProcessed(t, b.testProcessed, 1)

	// Unload B — r2 (queued, targeting B) must be released with an error.
	b.Unload(time.Second, "b")

	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("queued B request did not return after Unload(b)")
	}
	if w2.Code == http.StatusOK {
		t.Errorf("queued B request: expected non-OK status, got %d", w2.Code)
	}
	if got := pb.runCalls.Load(); got != 0 {
		t.Errorf("b.runCalls=%d want 0 (B should never have been started)", got)
	}

	// Release r1 so the test cleans up cleanly.
	a.markReady()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("r1 did not complete after a.markReady")
	}
}

func TestBaseRouter_FastPath(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()

	b := newTestBase(t, map[string]process.Process{"a": a}, &stubPlanner{})

	w := httptest.NewRecorder()
	b.ServeHTTP(w, newRequest("a"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.serveCalls.Load(); got != 1 {
		t.Errorf("serveCalls=%d want 1", got)
	}
	if got := a.runCalls.Load(); got != 0 {
		t.Errorf("runCalls=%d want 0 (fast path should not start)", got)
	}
}

func TestBaseRouter_OnDemandStart(t *testing.T) {
	a := newFakeProcess("a")
	a.autoReady = true

	b := newTestBase(t, map[string]process.Process{"a": a}, &stubPlanner{})

	w := httptest.NewRecorder()
	b.ServeHTTP(w, newRequest("a"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.runCalls.Load(); got != 1 {
		t.Errorf("runCalls=%d want 1", got)
	}
	if got := a.serveCalls.Load(); got != 1 {
		t.Errorf("serveCalls=%d want 1", got)
	}
}

func TestBaseRouter_ConcurrentSameModel(t *testing.T) {
	a := newFakeProcess("a")
	// autoReady=false so the swap parks on WaitReady until we release it.

	b := newTestBase(t, map[string]process.Process{"a": a}, &stubPlanner{})

	const N = 5
	var wg sync.WaitGroup
	codes := make([]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			b.ServeHTTP(w, newRequest("a"))
			codes[i] = w.Code
		}(i)
	}

	waitProcessed(t, b.testProcessed, N) // all N handlerReqs absorbed by run()
	<-a.runStarted                       // swap goroutine reached Run()
	a.markReady()
	wg.Wait()

	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("request %d: status=%d", i, c)
		}
	}
	if got := a.runCalls.Load(); got != 1 {
		t.Errorf("runCalls=%d want 1 (single swap should issue one Run)", got)
	}
	if got := a.serveCalls.Load(); got != N {
		t.Errorf("serveCalls=%d want %d", got, N)
	}
}

func TestBaseRouter_ContextCancel(t *testing.T) {
	a := newFakeProcess("a")
	// autoReady=false so swap parks forever until we mark ready.

	b := newTestBase(t, map[string]process.Process{"a": a}, &stubPlanner{})

	ctx, cancel := context.WithCancel(context.Background())
	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		b.ServeHTTP(w1, newRequestCtx(ctx, "a"))
		close(done1)
	}()

	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		b.ServeHTTP(w2, newRequest("a"))
		close(done2)
	}()

	waitProcessed(t, b.testProcessed, 2) // both requests joined the active swap
	<-a.runStarted

	cancel()
	select {
	case <-done1:
	case <-time.After(time.Second):
		t.Fatal("cancelled ServeHTTP did not return after ctx cancel")
	}

	a.markReady()
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("non-cancelled ServeHTTP did not complete after swap")
	}
	if w2.Code != http.StatusOK {
		t.Errorf("second request status=%d body=%q", w2.Code, w2.Body.String())
	}
}

func TestBaseRouter_QueuedDifferentModel(t *testing.T) {
	a := newFakeProcess("a")
	pa := newFakeProcess("b")

	// Loading b must stop a.
	planner := &stubPlanner{evict: map[string][]string{"b": {"a"}}}
	b := newTestBase(t, map[string]process.Process{"a": a, "b": pa}, planner)

	// First request starts a swap to A; A's autoReady=false so it parks.
	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		b.ServeHTTP(w1, newRequest("a"))
		close(done1)
	}()
	waitProcessed(t, b.testProcessed, 1)
	<-a.runStarted

	// Second request for B should queue while A's swap is in flight.
	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		b.ServeHTTP(w2, newRequest("b"))
		close(done2)
	}()
	waitProcessed(t, b.testProcessed, 1)

	if got := pa.runCalls.Load(); got != 0 {
		t.Errorf("b started early: runCalls=%d want 0 while A's swap is pending", got)
	}

	// Release A's swap. B's swap should then run.
	a.markReady()
	waitProcessed(t, b.testProcessed, 1) // swapDone for A → B's swap kicked off
	<-pa.runStarted

	select {
	case <-done1:
	case <-time.After(time.Second):
		t.Fatal("A request did not complete")
	}
	pa.markReady()
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("queued B request did not complete after A's swap")
	}
	if w2.Code != http.StatusOK {
		t.Errorf("B status=%d body=%q", w2.Code, w2.Body.String())
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1 (B's swap must stop A)", got)
	}
}

// TestBaseRouter_QueueCollation verifies that incoming requests of the form
// a, b, c, a, b, c collapse into three swaps (one per model) and that the
// second request for each model rides the fast path — either joining the
// active swap, or being pulled out of the queue when handleSwapDone promotes
// the next model.
func TestBaseRouter_QueueCollation(t *testing.T) {
	a := newFakeProcess("a")
	pb := newFakeProcess("b")
	pc := newFakeProcess("c")

	// Each model evicts the other two so all swaps are mutually exclusive.
	planner := &stubPlanner{evict: map[string][]string{
		"a": {"b", "c"},
		"b": {"a", "c"},
		"c": {"a", "b"},
	}}
	b := newTestBase(t, map[string]process.Process{"a": a, "b": pb, "c": pc}, planner)

	var (
		completedMu sync.Mutex
		completed   []string
	)
	record := func(id string) {
		completedMu.Lock()
		defer completedMu.Unlock()
		completed = append(completed, id)
	}

	ids := []string{"a", "b", "c", "a", "b", "c"}
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			b.ServeHTTP(w, newRequest(id))
			if w.Code != http.StatusOK {
				t.Errorf("%s: status=%d body=%q", id, w.Code, w.Body.String())
				return
			}
			record(id)
		}()
		// Wait for run() to absorb this request before launching the next,
		// so handlerCh receives them in launch order.
		waitProcessed(t, b.testProcessed, 1)
	}

	// All 6 are now parked in run()'s waiters/queue. Release each swap in
	// sequence, waiting deterministically for each promotion to fire.
	<-a.runStarted
	a.markReady()
	waitProcessed(t, b.testProcessed, 1) // swapDone(a) → b swap kicked off

	<-pb.runStarted
	pb.markReady()
	waitProcessed(t, b.testProcessed, 1) // swapDone(b) → c swap kicked off

	<-pc.runStarted
	pc.markReady()
	wg.Wait()

	if got := len(completed); got != 6 {
		t.Fatalf("completed=%v want 6", completed)
	}

	// run() fans out responses in model-grouped order (a1,a2 → b1,b2 → c1,c2)
	// but waiter goroutines may be scheduled in any order after their respond
	// channel fires, so completion order isn't deterministic. Per-model counts
	// (combined with the runCalls checks below) are sufficient to prove queue
	// collation collapsed each pair into a single swap.
	aDone, bDone, cDone := 0, 0, 0
	for _, id := range completed {
		switch id {
		case "a":
			aDone++
		case "b":
			bDone++
		case "c":
			cDone++
		}
	}
	if aDone != 2 || bDone != 2 || cDone != 2 {
		t.Errorf("per-model counts: a=%d b=%d c=%d, want 2 each (order=%v)", aDone, bDone, cDone, completed)
	}

	// Single swap per model — the second request for each must have ridden
	// the fast path (joined active swap or joined a queued sibling), not
	// triggered an extra Run.
	if got := a.runCalls.Load(); got != 1 {
		t.Errorf("a.runCalls=%d want 1", got)
	}
	if got := pb.runCalls.Load(); got != 1 {
		t.Errorf("b.runCalls=%d want 1", got)
	}
	if got := pc.runCalls.Load(); got != 1 {
		t.Errorf("c.runCalls=%d want 1", got)
	}
}

// TestBaseRouter_ConcurrentDisjointSwaps verifies that two requests with
// non-conflicting evict sets are loaded in parallel: both Run() calls happen
// before either process is marked ready.
func TestBaseRouter_ConcurrentDisjointSwaps(t *testing.T) {
	a := newFakeProcess("a")
	pb := newFakeProcess("b")

	// Empty evict sets for both: they can load in parallel.
	b := newTestBase(t, map[string]process.Process{"a": a, "b": pb}, &stubPlanner{})

	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		b.ServeHTTP(w1, newRequest("a"))
		close(done1)
	}()
	waitProcessed(t, b.testProcessed, 1)

	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		b.ServeHTTP(w2, newRequest("b"))
		close(done2)
	}()
	waitProcessed(t, b.testProcessed, 1)

	// Both swaps must have reached Run() before either is marked ready —
	// proves they ran in parallel rather than serializing.
	<-a.runStarted
	<-pb.runStarted

	a.markReady()
	pb.markReady()

	select {
	case <-done1:
	case <-time.After(time.Second):
		t.Fatal("request A did not complete")
	}
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("request B did not complete")
	}

	if w1.Code != http.StatusOK {
		t.Errorf("A status=%d body=%q", w1.Code, w1.Body.String())
	}
	if w2.Code != http.StatusOK {
		t.Errorf("B status=%d body=%q", w2.Code, w2.Body.String())
	}
	if got := a.stopCalls.Load(); got != 0 {
		t.Errorf("a.stopCalls=%d want 0 (parallel swap, no eviction)", got)
	}
	if got := pb.stopCalls.Load(); got != 0 {
		t.Errorf("b.stopCalls=%d want 0 (parallel swap, no eviction)", got)
	}
}

// TestBaseRouter_QueueDrainPromotesMultiple verifies that completing one swap
// unblocks every queued request that no longer collides — they all start in
// parallel rather than one-per-completion.
func TestBaseRouter_QueueDrainPromotesMultiple(t *testing.T) {
	a := newFakeProcess("a")
	pb := newFakeProcess("b")
	pc := newFakeProcess("c")

	// A's swap evicts both B and C, so B and C must queue. Once A finishes
	// B and C themselves have empty evict sets, so they can start together.
	planner := &stubPlanner{evict: map[string][]string{
		"a": {"b", "c"},
	}}
	b := newTestBase(t, map[string]process.Process{"a": a, "b": pb, "c": pc}, planner)

	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		b.ServeHTTP(w1, newRequest("a"))
		close(done1)
	}()
	waitProcessed(t, b.testProcessed, 1)
	<-a.runStarted

	// B and C arrive while A is loading. evict_b and evict_c are empty,
	// but collidesWith returns true because they appear in A's evict set.
	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		b.ServeHTTP(w2, newRequest("b"))
		close(done2)
	}()
	waitProcessed(t, b.testProcessed, 1)

	w3 := httptest.NewRecorder()
	done3 := make(chan struct{})
	go func() {
		b.ServeHTTP(w3, newRequest("c"))
		close(done3)
	}()
	waitProcessed(t, b.testProcessed, 1)

	if got := pb.runCalls.Load(); got != 0 {
		t.Errorf("b started early: runCalls=%d", got)
	}
	if got := pc.runCalls.Load(); got != 0 {
		t.Errorf("c started early: runCalls=%d", got)
	}

	// Release A. The swapDone handler should drain the queue and start
	// both B and C in parallel.
	a.markReady()
	waitProcessed(t, b.testProcessed, 1) // swapDone(A) → drainQueue starts B and C
	<-pb.runStarted
	<-pc.runStarted

	pb.markReady()
	pc.markReady()

	for i, ch := range []chan struct{}{done1, done2, done3} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("request %d did not complete", i)
		}
	}
}

// TestBaseRouter_Shutdown_FailsAllInFlight verifies that shutdown returns
// the shutdown error to every waiter on every active swap AND to every
// queued request.
func TestBaseRouter_Shutdown_FailsAllInFlight(t *testing.T) {
	a := newFakeProcess("a")
	pb := newFakeProcess("b")
	pc := newFakeProcess("c")

	// a and b load in parallel (empty evicts). c collides with both.
	planner := &stubPlanner{evict: map[string][]string{
		"c": {"a", "b"},
	}}
	b := newTestBase(t, map[string]process.Process{"a": a, "b": pb, "c": pc}, planner)

	const waitersPer = 2
	var wg sync.WaitGroup
	codes := make([]int, 0, 2*waitersPer+1)
	var codesMu sync.Mutex
	record := func(code int) {
		codesMu.Lock()
		codes = append(codes, code)
		codesMu.Unlock()
	}

	launch := func(model string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			b.ServeHTTP(w, newRequest(model))
			record(w.Code)
		}()
	}

	// Active swaps for a and b, each with 2 waiters.
	for i := 0; i < waitersPer; i++ {
		launch("a")
		waitProcessed(t, b.testProcessed, 1)
	}
	for i := 0; i < waitersPer; i++ {
		launch("b")
		waitProcessed(t, b.testProcessed, 1)
	}
	// c collides with both → queues.
	launch("c")
	waitProcessed(t, b.testProcessed, 1)

	<-a.runStarted
	<-pb.runStarted

	if err := b.Shutdown(time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	wg.Wait()

	codesMu.Lock()
	defer codesMu.Unlock()
	if len(codes) != 2*waitersPer+1 {
		t.Fatalf("got %d responses, want %d", len(codes), 2*waitersPer+1)
	}
	for i, c := range codes {
		if c == http.StatusOK {
			t.Errorf("response %d: status=%d, want non-200 (shutdown)", i, c)
		}
	}
}

// TestBaseRouter_NoSwapWhileServing verifies that an already-loaded model
// is not stopped to satisfy another model's swap while it is still handling
// a request.
//
// Sequence:
//  1. r1 (A) — A loads; ServeHTTP enters and is pinned via serveBlock.
//  2. r2 (B, planner: B evicts A) — must NOT cause A.Stop while r1 is live.
//  3. r3 (A) — arrives next; the existing code queues it because B's swap
//     intent collides with A.
//  4. r1 released — A finishes r1, then r3 is served by A.
//  5. B's swap then proceeds; r2 is served by B.
//
// fakeProcess.stoppedWhileServing flips true if Stop is ever called while
// a ServeHTTP is in flight — a direct, race-free signal of the violation.
func TestBaseRouter_NoSwapWhileServing(t *testing.T) {
	a := newFakeProcess("a")
	// autoReady left false: we markReady manually after observing runStarted,
	// so autoReady's setState(Ready) cannot race with a later Stop and leave
	// A in Ready, masking the bug.
	a.serveBlock = make(chan struct{})
	pb := newFakeProcess("b")
	// Same reasoning for B: park its swap on WaitReady until we choose.

	planner := &stubPlanner{evict: map[string][]string{"b": {"a"}}}
	b := newTestBase(t, map[string]process.Process{"a": a, "b": pb}, planner)

	// r1 — load A and enter its ServeHTTP (which blocks on serveBlock).
	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		b.ServeHTTP(w1, newRequest("a"))
		close(done1)
	}()
	waitProcessed(t, b.testProcessed, 1) // handlerReq for r1
	<-a.runStarted
	a.markReady()
	waitProcessed(t, b.testProcessed, 1) // swapDone for A
	<-a.serveStarted

	// r2 — would evict A. A must not be stopped while r1 is in flight.
	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		b.ServeHTTP(w2, newRequest("b"))
		close(done2)
	}()
	waitProcessed(t, b.testProcessed, 1)

	// r3 — another request for A, arrives behind r2 and queues because
	// B's swap intent (which evicts A) is recorded as active.
	w3 := httptest.NewRecorder()
	done3 := make(chan struct{})
	go func() {
		b.ServeHTTP(w3, newRequest("a"))
		close(done3)
	}()
	waitProcessed(t, b.testProcessed, 1)

	// Release r1 (and r3 if it is fast-pathed onto the still-loaded A).
	// The router must hold off B's swap until A has drained.
	close(a.serveBlock)

	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("r1 did not complete after serveBlock release")
	}

	// Wait for B.Run before marking it ready: markReady before Run would
	// skip the Run path entirely and leave pb.runCalls at 0. In a correct
	// implementation B's swap only starts after A has drained; in the
	// current implementation it has already started — either way runStarted
	// fires.
	<-pb.runStarted
	pb.markReady()

	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("r2 did not complete after B marked ready")
	}
	select {
	case <-done3:
	case <-time.After(2 * time.Second):
		t.Fatal("r3 did not complete")
	}

	if w1.Code != http.StatusOK || w2.Code != http.StatusOK || w3.Code != http.StatusOK {
		t.Fatalf("statuses: w1=%d w2=%d w3=%d", w1.Code, w2.Code, w3.Code)
	}
	if w1.Body.String() != "ok:a" {
		t.Errorf("r1 body=%q want ok:a", w1.Body.String())
	}
	if w3.Body.String() != "ok:a" {
		t.Errorf("r3 body=%q want ok:a (r3 must be served by A)", w3.Body.String())
	}
	if w2.Body.String() != "ok:b" {
		t.Errorf("r2 body=%q want ok:b", w2.Body.String())
	}
	if a.stoppedWhileServing.Load() {
		t.Errorf("A.Stop was called while A was still handling a request — the router swapped out a busy process")
	}
}

func TestBaseRouter_ModelNotFound(t *testing.T) {
	a := newFakeProcess("a")
	b := newTestBase(t, map[string]process.Process{"a": a}, &stubPlanner{})

	w := httptest.NewRecorder()
	b.ServeHTTP(w, newRequest("unknown"))

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want %d body=%q", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestBaseRouter_Shutdown_StopsAllProcesses(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0)
	pb := newFakeProcess("b")
	pb.markReady()
	go pb.Run(0)

	b := newTestBase(t, map[string]process.Process{"a": a, "b": pb}, &stubPlanner{})

	if err := b.Shutdown(time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1", got)
	}
	if got := pb.stopCalls.Load(); got != 1 {
		t.Errorf("b.stopCalls=%d want 1", got)
	}

	// Subsequent ServeHTTP should report 5xx.
	w := httptest.NewRecorder()
	b.ServeHTTP(w, newRequest("a"))
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusServiceUnavailable {
		t.Errorf("post-shutdown status=%d want 5xx body=%q", w.Code, w.Body.String())
	}

	// Second Shutdown should report already in progress.
	if err := b.Shutdown(0); err == nil {
		t.Errorf("second Shutdown returned nil, want error")
	}
}
