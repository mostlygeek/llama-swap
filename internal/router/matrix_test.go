package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// newTestMatrix builds a Matrix router from supplied processes, bypassing
// NewMatrix's call to process.New.
func newTestMatrix(t *testing.T, conf config.Config, expanded []config.ExpandedSet, evictCosts map[string]int, processes map[string]process.Process) *Matrix {
	t.Helper()
	logger := logmon.NewWriter(io.Discard)
	planner := &matrixPlanner{
		solver:    newMatrixSolver(expanded, evictCosts),
		processes: processes,
		logger:    logger,
	}
	base := newBaseRouter("matrix", conf, processes, planner, logger)
	base.testProcessed = make(chan struct{}, 64)
	r := &Matrix{baseRouter: base}
	go base.run()
	t.Cleanup(func() {
		if !r.shuttingDown.Load() {
			_ = r.Shutdown(time.Second)
		}
	})
	return r
}

func baseMatrixConfig() config.Config {
	return config.Config{
		HealthCheckTimeout: 5,
		Matrix:             &config.MatrixConfig{},
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

// TestMatrix_CoexistingSetParallel verifies that two models that share an
// expanded set load in parallel — the solver returns empty Evict for both,
// the collision predicate clears them, and both swaps run together.
func TestMatrix_CoexistingSetParallel(t *testing.T) {
	a := newFakeProcess("a")
	pb := newFakeProcess("b")

	expanded := []config.ExpandedSet{
		{SetName: "s_ab", DSL: "a & b", Models: []string{"a", "b"}},
	}
	r := newTestMatrix(t, baseMatrixConfig(), expanded, nil, map[string]process.Process{"a": a, "b": pb})

	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		r.ServeHTTP(w1, newRequest("a"))
		close(done1)
	}()
	waitProcessed(t, r.testProcessed, 1)

	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		r.ServeHTTP(w2, newRequest("b"))
		close(done2)
	}()
	waitProcessed(t, r.testProcessed, 1)

	<-a.runStarted
	<-pb.runStarted

	a.markReady()
	pb.markReady()

	for i, ch := range []chan struct{}{done1, done2} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("request %d did not complete", i)
		}
	}
	if got := a.stopCalls.Load(); got != 0 {
		t.Errorf("a.stopCalls=%d want 0 (coexists with b)", got)
	}
	if got := pb.stopCalls.Load(); got != 0 {
		t.Errorf("b.stopCalls=%d want 0 (coexists with a)", got)
	}
}

// TestMatrix_IncompatibleQueues verifies that the second request for a model
// that cannot coexist with the in-flight first model queues until the first
// completes, and then evicts it. This exercises the alsoRunning hint via the
// matrix solver's union into runningSet.
func TestMatrix_IncompatibleQueues(t *testing.T) {
	a := newFakeProcess("a")
	pb := newFakeProcess("b")

	expanded := []config.ExpandedSet{
		{SetName: "s_a", DSL: "a", Models: []string{"a"}},
		{SetName: "s_b", DSL: "b", Models: []string{"b"}},
	}
	r := newTestMatrix(t, baseMatrixConfig(), expanded, nil, map[string]process.Process{"a": a, "b": pb})

	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		r.ServeHTTP(w1, newRequest("a"))
		close(done1)
	}()
	waitProcessed(t, r.testProcessed, 1)

	// B arrives before A transitions to StateStarting. The solver sees A via
	// alsoRunning and returns evict=[a], so collidesWith forces B to queue.
	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		r.ServeHTTP(w2, newRequest("b"))
		close(done2)
	}()
	waitProcessed(t, r.testProcessed, 1)

	if got := pb.runCalls.Load(); got != 0 {
		t.Errorf("b started in parallel: runCalls=%d want 0", got)
	}

	<-a.runStarted
	a.markReady()
	waitProcessed(t, r.testProcessed, 1) // swapDone(a) → b promoted, evicts a
	<-pb.runStarted
	pb.markReady()

	for i, ch := range []chan struct{}{done1, done2} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("request %d did not complete", i)
		}
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1 (b's swap must stop a)", got)
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
