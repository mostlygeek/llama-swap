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

// newTestMatrix builds a MatrixRouter from supplied processes, bypassing
// NewMatrix's call to process.New. The fakeProcess helper from group_test.go
// is reused for the underlying processes.
func newTestMatrix(t *testing.T, conf config.Config, expanded []config.ExpandedSet, evictCosts map[string]int, processes map[string]process.Process) *MatrixRouter {
	t.Helper()
	shutdownCtx, shutdownFn := context.WithCancel(context.Background())
	r := &MatrixRouter{
		config:        conf,
		solver:        newMatrixSolver(expanded, evictCosts),
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
	go r.run()
	t.Cleanup(func() {
		if !r.shuttingDown.Load() {
			_ = r.Shutdown(time.Second)
		}
	})
	return r
}

func waitProcessedMatrix(t *testing.T, r *MatrixRouter, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-r.testProcessed:
		case <-time.After(2 * time.Second):
			t.Fatalf("waitProcessedMatrix: only %d/%d events received", i, n)
		}
	}
}

func baseMatrixConfig() config.Config {
	return config.Config{
		HealthCheckTimeout: 5,
		Matrix:             &config.MatrixConfig{},
	}
}

func TestMatrix_ServeHTTP_FastPath(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()

	expanded := []config.ExpandedSet{
		{SetName: "s1", DSL: "a", Models: []string{"a"}},
	}
	r := newTestMatrix(t, baseMatrixConfig(), expanded, nil, map[string]process.Process{"a": a})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, newRequest("a"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.serveCalls.Load(); got != 1 {
		t.Errorf("serveCalls=%d want 1", got)
	}
	if got := a.runCalls.Load(); got != 0 {
		t.Errorf("runCalls=%d want 0 (fast path)", got)
	}
}

func TestMatrix_ServeHTTP_OnDemandStart(t *testing.T) {
	a := newFakeProcess("a")
	a.autoReady = true

	expanded := []config.ExpandedSet{
		{SetName: "s1", DSL: "a", Models: []string{"a"}},
	}
	r := newTestMatrix(t, baseMatrixConfig(), expanded, nil, map[string]process.Process{"a": a})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, newRequest("a"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.runCalls.Load(); got != 1 {
		t.Errorf("runCalls=%d want 1", got)
	}
}

// TestMatrix_SwapEvictsConflicting verifies that loading a model triggers
// eviction of running models that are not in any shared set with it.
func TestMatrix_SwapEvictsConflicting(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0) // park a Run goroutine so Stop has something to release

	b := newFakeProcess("b")
	b.autoReady = true

	// Two single-model sets: a and b never coexist, so loading b must evict a.
	expanded := []config.ExpandedSet{
		{SetName: "s_a", DSL: "a", Models: []string{"a"}},
		{SetName: "s_b", DSL: "b", Models: []string{"b"}},
	}
	r := newTestMatrix(t, baseMatrixConfig(), expanded, nil, map[string]process.Process{"a": a, "b": b})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, newRequest("b"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1", got)
	}
	if got := b.runCalls.Load(); got != 1 {
		t.Errorf("b.runCalls=%d want 1", got)
	}
}

// TestMatrix_CoexistInSet verifies that a model is not evicted when the target
// shares a set with it (the fast path applies if the target is already ready).
func TestMatrix_CoexistInSet(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0)

	b := newFakeProcess("b")
	b.autoReady = true

	// Both fit in s_ab, so b's swap should not stop a.
	expanded := []config.ExpandedSet{
		{SetName: "s_ab", DSL: "a & b", Models: []string{"a", "b"}},
	}
	r := newTestMatrix(t, baseMatrixConfig(), expanded, nil, map[string]process.Process{"a": a, "b": b})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, newRequest("b"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.stopCalls.Load(); got != 0 {
		t.Errorf("a.stopCalls=%d want 0 (coexists with b)", got)
	}
	if got := b.runCalls.Load(); got != 1 {
		t.Errorf("b.runCalls=%d want 1", got)
	}
}

// TestMatrix_ConcurrentSameModel pins that N concurrent requests for the same
// model trigger a single swap and all are served.
func TestMatrix_ConcurrentSameModel(t *testing.T) {
	a := newFakeProcess("a")

	expanded := []config.ExpandedSet{
		{SetName: "s_a", DSL: "a", Models: []string{"a"}},
	}
	r := newTestMatrix(t, baseMatrixConfig(), expanded, nil, map[string]process.Process{"a": a})

	const N = 5
	var wg sync.WaitGroup
	codes := make([]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			r.ServeHTTP(w, newRequest("a"))
			codes[i] = w.Code
		}(i)
	}

	waitProcessedMatrix(t, r, N)
	<-a.runStarted
	a.markReady()
	wg.Wait()

	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("request %d: status=%d", i, c)
		}
	}
	if got := a.runCalls.Load(); got != 1 {
		t.Errorf("runCalls=%d want 1", got)
	}
	if got := a.serveCalls.Load(); got != N {
		t.Errorf("serveCalls=%d want %d", got, N)
	}
}

// TestMatrix_QueuedDifferentModel verifies that a request for a different
// model queues during an in-flight swap and is promoted afterward.
func TestMatrix_QueuedDifferentModel(t *testing.T) {
	a := newFakeProcess("a")
	b := newFakeProcess("b")

	expanded := []config.ExpandedSet{
		{SetName: "s_a", DSL: "a", Models: []string{"a"}},
		{SetName: "s_b", DSL: "b", Models: []string{"b"}},
	}
	r := newTestMatrix(t, baseMatrixConfig(), expanded, nil, map[string]process.Process{"a": a, "b": b})

	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		r.ServeHTTP(w1, newRequest("a"))
		close(done1)
	}()
	waitProcessedMatrix(t, r, 1)
	<-a.runStarted

	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		r.ServeHTTP(w2, newRequest("b"))
		close(done2)
	}()
	waitProcessedMatrix(t, r, 1)

	if got := b.runCalls.Load(); got != 0 {
		t.Errorf("b started early: runCalls=%d want 0", got)
	}

	a.markReady()
	waitProcessedMatrix(t, r, 1) // swapDone(a) → b promoted
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
		t.Fatal("queued B request did not complete")
	}
	if w2.Code != http.StatusOK {
		t.Errorf("B status=%d body=%q", w2.Code, w2.Body.String())
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1", got)
	}
}

func TestMatrix_ServeHTTP_ModelNotFound(t *testing.T) {
	a := newFakeProcess("a")
	expanded := []config.ExpandedSet{
		{SetName: "s_a", DSL: "a", Models: []string{"a"}},
	}
	r := newTestMatrix(t, baseMatrixConfig(), expanded, nil, map[string]process.Process{"a": a})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, newRequest("unknown"))

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want %d", w.Code, http.StatusNotFound)
	}
}

func TestMatrix_Shutdown_StopsAllProcesses(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0)
	b := newFakeProcess("b")
	b.markReady()
	go b.Run(0)

	expanded := []config.ExpandedSet{
		{SetName: "s_ab", DSL: "a & b", Models: []string{"a", "b"}},
	}
	r := newTestMatrix(t, baseMatrixConfig(), expanded, nil, map[string]process.Process{"a": a, "b": b})

	if err := r.Shutdown(time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1", got)
	}
	if got := b.stopCalls.Load(); got != 1 {
		t.Errorf("b.stopCalls=%d want 1", got)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, newRequest("a"))
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusServiceUnavailable {
		t.Errorf("post-shutdown status=%d want 5xx", w.Code)
	}

	if err := r.Shutdown(0); err == nil {
		t.Errorf("second Shutdown returned nil, want error")
	}
}

// TestMatrixSolver_TieBreakDefinitionOrder pins the solver's tie-break rule:
// when multiple candidate sets have equal eviction cost, the earlier-defined
// set wins.
func TestMatrixSolver_TieBreakDefinitionOrder(t *testing.T) {
	expanded := []config.ExpandedSet{
		{SetName: "first", DSL: "a & b", Models: []string{"a", "b"}},
		{SetName: "second", DSL: "a & c", Models: []string{"a", "c"}},
	}
	s := newMatrixSolver(expanded, nil)

	// No models running, request "a": both sets have cost 0 and contain a.
	// Definition order: "first" wins.
	result := s.Solve("a", nil)
	if result.SetName != "first" {
		t.Errorf("SetName=%q want %q", result.SetName, "first")
	}
}

// TestMatrixSolver_EvictCostsPreferred verifies that higher evict costs steer
// the solver toward a cheaper set.
func TestMatrixSolver_EvictCostsPreferred(t *testing.T) {
	// b is expensive to evict; c is cheap. Request "a" with both b and c
	// running. The solver should pick the set that keeps b.
	expanded := []config.ExpandedSet{
		{SetName: "a_with_c", DSL: "a & c", Models: []string{"a", "c"}}, // would evict b (cost 10)
		{SetName: "a_with_b", DSL: "a & b", Models: []string{"a", "b"}}, // would evict c (cost 1)
	}
	s := newMatrixSolver(expanded, map[string]int{"b": 10, "c": 1})

	result := s.Solve("a", []string{"b", "c"})
	if result.SetName != "a_with_b" {
		t.Errorf("SetName=%q want %q (keep expensive b)", result.SetName, "a_with_b")
	}
	if len(result.Evict) != 1 || result.Evict[0] != "c" {
		t.Errorf("Evict=%v want [c]", result.Evict)
	}
}
