package router

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

// fakeProcess is an in-memory implementation of process.Process used to drive
// Group through its state machine without spawning real upstreams.
type fakeProcess struct {
	id string

	mu         sync.Mutex
	state      process.ProcessState
	readyCh    chan struct{}
	stopCh     chan struct{}
	runStarted chan struct{} // closed on the first Run call

	autoReady bool

	runCalls   atomic.Int32
	stopCalls  atomic.Int32
	serveCalls atomic.Int32
}

func newFakeProcess(id string) *fakeProcess {
	return &fakeProcess{
		id:         id,
		state:      process.StateStopped,
		readyCh:    make(chan struct{}),
		stopCh:     make(chan struct{}),
		runStarted: make(chan struct{}),
	}
}

func (f *fakeProcess) setState(s process.ProcessState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = s
	if s == process.StateReady {
		select {
		case <-f.readyCh:
		default:
			close(f.readyCh)
		}
	}
}

func (f *fakeProcess) State() process.ProcessState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeProcess) markReady() { f.setState(process.StateReady) }

func (f *fakeProcess) Run(_ time.Duration) error {
	f.runCalls.Add(1)
	f.mu.Lock()
	if f.state != process.StateStopped {
		s := f.state
		f.mu.Unlock()
		return fmt.Errorf("fakeProcess %s: Run called while %s", f.id, s)
	}
	f.state = process.StateStarting
	sc := f.stopCh
	select {
	case <-f.runStarted:
	default:
		close(f.runStarted)
	}
	f.mu.Unlock()

	if f.autoReady {
		f.setState(process.StateReady)
	}
	<-sc
	return nil
}

func (f *fakeProcess) Stop(_ time.Duration) error {
	f.stopCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == process.StateStopped {
		return nil
	}
	f.state = process.StateStopped
	select {
	case <-f.stopCh:
	default:
		close(f.stopCh)
	}
	return nil
}

func (f *fakeProcess) WaitReady(ctx context.Context) error {
	f.mu.Lock()
	if f.state == process.StateReady {
		f.mu.Unlock()
		return nil
	}
	rc := f.readyCh
	f.mu.Unlock()
	select {
	case <-rc:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeProcess) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.serveCalls.Add(1)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok:%s", f.id)
}

// newTestGroup builds a Group directly from the supplied processes and config,
// bypassing NewGroup's call to process.New.
func newTestGroup(t *testing.T, conf config.Config, processes map[string]process.Process) *Group {
	t.Helper()
	modelToGroup := make(map[string]string)
	for gid, gcfg := range conf.Groups {
		for _, mid := range gcfg.Members {
			modelToGroup[mid] = gid
		}
	}
	shutdownCtx, shutdownFn := context.WithCancel(context.Background())
	g := &Group{
		config:        conf,
		modelToGroup:  modelToGroup,
		processes:     processes,
		logger:        logmon.NewWriter(io.Discard),
		shutdownCtx:   shutdownCtx,
		shutdownFn:    shutdownFn,
		handlerCh:     make(chan handlerReq),
		shutdownCh:    make(chan shutdownReq),
		swapDoneCh:    make(chan swapDone),
		runDone:       make(chan struct{}),
		testProcessed: make(chan struct{}, 64),
	}
	go g.run()
	t.Cleanup(func() {
		if !g.shuttingDown.Load() {
			_ = g.Shutdown(time.Second)
		}
	})
	return g
}

// waitProcessed drains n events from g.testProcessed, fataling on timeout.
// One event fires per handlerReq or swapDone fully absorbed by run().
func waitProcessed(t *testing.T, g *Group, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-g.testProcessed:
		case <-time.After(2 * time.Second):
			t.Fatalf("waitProcessed: only %d/%d events received", i, n)
		}
	}
}

func newRequest(model string) *http.Request {
	body := fmt.Sprintf(`{"model":%q}`, model)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func newRequestCtx(ctx context.Context, model string) *http.Request {
	return newRequest(model).WithContext(ctx)
}

func TestGroup_NewGroup_DuplicateMembership(t *testing.T) {
	conf := config.Config{
		Groups: map[string]config.GroupConfig{
			"g1": {Swap: true, Members: []string{"a"}},
			"g2": {Swap: true, Members: []string{"a"}},
		},
		Models: map[string]config.ModelConfig{
			"a": {},
		},
	}
	log := logmon.NewWriter(io.Discard)
	if _, err := NewGroup(conf, log, log); err == nil {
		t.Fatalf("expected error for duplicate membership")
	}
}

func TestGroup_ServeHTTP_FastPath(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: true, Exclusive: true, Members: []string{"a"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("a"))

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

func TestGroup_ServeHTTP_OnDemandStart(t *testing.T) {
	a := newFakeProcess("a")
	a.autoReady = true

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: true, Exclusive: true, Members: []string{"a"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("a"))

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

func TestGroup_ServeHTTP_SwapStopsPrevious(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0) // park a Run goroutine so Stop has something to release

	b := newFakeProcess("b")
	b.autoReady = true

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: true, Exclusive: true, Members: []string{"a", "b"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("b"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1", got)
	}
	if got := b.runCalls.Load(); got != 1 {
		t.Errorf("b.runCalls=%d want 1", got)
	}
	if got := b.serveCalls.Load(); got != 1 {
		t.Errorf("b.serveCalls=%d want 1", got)
	}
}

func TestGroup_NonSwapGroup_NoStop(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()

	b := newFakeProcess("b")
	b.autoReady = true

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: false, Exclusive: false, Members: []string{"a", "b"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("b"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.stopCalls.Load(); got != 0 {
		t.Errorf("a.stopCalls=%d want 0 (swap=false should not stop siblings)", got)
	}
	if got := b.runCalls.Load(); got != 1 {
		t.Errorf("b.runCalls=%d want 1", got)
	}
}

func TestGroup_CrossGroupExclusive(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0)

	b := newFakeProcess("b")
	b.autoReady = true

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g1": {Swap: true, Exclusive: true, Members: []string{"a"}},
			"g2": {Swap: true, Exclusive: true, Members: []string{"b"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("b"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1 (cross-group exclusive must stop)", got)
	}
}

// TestGroup_NonExclusiveDoesNotUnloadExclusive pins a backwards-compatible
// gotcha from the original ProcessGroup: when a model in a non-exclusive group
// is loaded, any running exclusive group keeps running. The two coexist.
func TestGroup_NonExclusiveDoesNotUnloadExclusive(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0)

	b := newFakeProcess("b")
	b.autoReady = true

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g1": {Swap: true, Exclusive: true, Members: []string{"a"}},
			"g2": {Swap: true, Exclusive: false, Members: []string{"b"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("b"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.stopCalls.Load(); got != 0 {
		t.Errorf("a.stopCalls=%d want 0 (non-exclusive target must not unload exclusive group)", got)
	}
	if a.State() != process.StateStarting && a.State() != process.StateReady {
		t.Errorf("a state=%s want still running", a.State())
	}
	if got := b.runCalls.Load(); got != 1 {
		t.Errorf("b.runCalls=%d want 1", got)
	}
}

func TestGroup_ServeHTTP_ConcurrentSameModel(t *testing.T) {
	a := newFakeProcess("a")
	// autoReady=false so the swap parks on WaitReady until we release it.

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: true, Exclusive: true, Members: []string{"a"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a})

	const N = 5
	var wg sync.WaitGroup
	codes := make([]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			g.ServeHTTP(w, newRequest("a"))
			codes[i] = w.Code
		}(i)
	}

	waitProcessed(t, g, N) // all N handlerReqs absorbed by run()
	<-a.runStarted         // swap goroutine reached Run()
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

func TestGroup_ServeHTTP_ContextCancel(t *testing.T) {
	a := newFakeProcess("a")
	// autoReady=false so swap parks forever until we mark ready.

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: true, Exclusive: true, Members: []string{"a"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a})

	ctx, cancel := context.WithCancel(context.Background())
	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		g.ServeHTTP(w1, newRequestCtx(ctx, "a"))
		close(done1)
	}()

	// Second request that we'll let succeed normally.
	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		g.ServeHTTP(w2, newRequest("a"))
		close(done2)
	}()

	waitProcessed(t, g, 2) // both requests now joined the active swap
	<-a.runStarted

	// Cancel the first request — its ServeHTTP should return promptly even
	// though the swap is still in flight.
	cancel()
	select {
	case <-done1:
	case <-time.After(time.Second):
		t.Fatal("cancelled ServeHTTP did not return after ctx cancel")
	}

	// The second waiter should still complete after the swap finishes.
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

func TestGroup_ServeHTTP_QueuedDifferentModel(t *testing.T) {
	a := newFakeProcess("a")
	b := newFakeProcess("b")

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: true, Exclusive: true, Members: []string{"a", "b"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b})

	// First request starts a swap to A; A's autoReady=false so it parks.
	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		g.ServeHTTP(w1, newRequest("a"))
		close(done1)
	}()
	waitProcessed(t, g, 1)
	<-a.runStarted

	// Second request for B should queue while A's swap is in flight.
	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		g.ServeHTTP(w2, newRequest("b"))
		close(done2)
	}()
	waitProcessed(t, g, 1)

	if got := b.runCalls.Load(); got != 0 {
		t.Errorf("b started early: runCalls=%d want 0 while A's swap is pending", got)
	}

	// Release A's swap. B's swap should then run.
	a.markReady()
	waitProcessed(t, g, 1) // swapDone for A → B's swap kicked off
	<-b.runStarted

	select {
	case <-done1:
	case <-time.After(time.Second):
		t.Fatal("A request did not complete")
	}
	b.markReady()
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

// TestGroup_ServeHTTP_QueueCollation verifies that incoming requests of the
// form a, b, c, a, b, c collapse into three swaps (one per model) and are
// served in the order a, a, b, b, c, c. The second request for each model
// rides the fast path: it either joins the active swap or is pulled out of
// the queue when handleSwapDone promotes the next model.
func TestGroup_ServeHTTP_QueueCollation(t *testing.T) {
	a := newFakeProcess("a")
	b := newFakeProcess("b")
	c := newFakeProcess("c")

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: true, Exclusive: true, Members: []string{"a", "b", "c"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b, "c": c})

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
			g.ServeHTTP(w, newRequest(id))
			if w.Code != http.StatusOK {
				t.Errorf("%s: status=%d body=%q", id, w.Code, w.Body.String())
				return
			}
			record(id)
		}()
		// Wait for run() to absorb this request before launching the next,
		// so handlerCh receives them in launch order.
		waitProcessed(t, g, 1)
	}

	// All 6 are now parked in run()'s waiters/queue. Release each swap in
	// sequence, waiting deterministically for each promotion to fire.
	<-a.runStarted
	a.markReady()
	waitProcessed(t, g, 1) // swapDone(a) → b swap kicked off

	<-b.runStarted
	b.markReady()
	waitProcessed(t, g, 1) // swapDone(b) → c swap kicked off

	<-c.runStarted
	c.markReady()
	wg.Wait()

	if got := len(completed); got != 6 {
		t.Fatalf("completed=%v want 6", completed)
	}

	// Within-model serve order is not deterministic (two waiters race on
	// invoking ServeHTTP after both receive the handler), but the
	// model-level grouping must hold: all a's before any b, all b's before
	// any c.
	aDone, bDone, cDone := 0, 0, 0
	for _, id := range completed {
		switch id {
		case "a":
			aDone++
			if bDone > 0 || cDone > 0 {
				t.Errorf("'a' served after b/c: order=%v", completed)
			}
		case "b":
			bDone++
			if cDone > 0 {
				t.Errorf("'b' served after c: order=%v", completed)
			}
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
	if got := b.runCalls.Load(); got != 1 {
		t.Errorf("b.runCalls=%d want 1", got)
	}
	if got := c.runCalls.Load(); got != 1 {
		t.Errorf("c.runCalls=%d want 1", got)
	}
}

func TestGroup_ServeHTTP_ModelNotFound(t *testing.T) {
	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: true, Exclusive: true, Members: []string{"a"}},
		},
	}
	a := newFakeProcess("a")
	g := newTestGroup(t, conf, map[string]process.Process{"a": a})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("unknown"))

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want %d body=%q", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestGroup_Shutdown_StopsAllProcesses(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0)
	b := newFakeProcess("b")
	b.markReady()
	go b.Run(0)

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: false, Exclusive: false, Members: []string{"a", "b"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b})

	if err := g.Shutdown(time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1", got)
	}
	if got := b.stopCalls.Load(); got != 1 {
		t.Errorf("b.stopCalls=%d want 1", got)
	}

	// Subsequent ServeHTTP should report 503.
	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("a"))
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusServiceUnavailable {
		t.Errorf("post-shutdown status=%d want 5xx body=%q", w.Code, w.Body.String())
	}

	// Second Shutdown should report already in progress.
	if err := g.Shutdown(0); err == nil {
		t.Errorf("second Shutdown returned nil, want error")
	}
}
