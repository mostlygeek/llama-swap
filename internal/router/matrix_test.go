package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/config"
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
