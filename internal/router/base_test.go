package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

// stubPlanner is a swapPlanner that returns a fixed eviction list per target
// and never logs. It lets the base-router tests cover shared run-loop
// behaviour without dragging in either real router's eviction rules.
type stubPlanner struct {
	evict map[string][]string
}

func (s *stubPlanner) EvictionFor(target string) []string {
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
