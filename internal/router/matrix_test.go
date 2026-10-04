package router

import (
	"bytes"
	"fmt"
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
func newTestMatrix(t *testing.T, conf config.Config, sets config.OrderedSets, evictCosts map[string]int, processes map[string]process.Process) *Matrix {
	t.Helper()
	models := make(map[string]config.ModelConfig, len(processes))
	for model := range processes {
		models[model] = config.ModelConfig{}
	}
	matrix := &config.MatrixConfig{
		EvictCosts: evictCosts,
		Sets:       sets,
	}
	if err := config.ValidateMatrix(matrix, models); err != nil {
		t.Fatalf("ValidateMatrix: %v", err)
	}

	logger := logmon.NewWriter(io.Discard)
	tracker := newLoadCostTracker(matrix.ResolvedEvictCosts(), logger)
	swapper := &matrixSwapper{
		solver: newMatrixSolver(matrix.Program(), tracker.EvictCosts, config.ReclaimMinimal),
		logger: logger,
	}
	base, err := newBaseRouter("matrix", conf, processes, logger, swapper)
	if err != nil {
		t.Fatalf("newBaseRouter: %v", err)
	}
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
	sets := config.OrderedSets{
		{Name: "s_a", DSL: "a"},
		{Name: "s_b", DSL: "b"},
	}
	r := newTestMatrix(t, baseMatrixConfig(), sets, nil, map[string]process.Process{"a": a, "b": b})

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
	sets := config.OrderedSets{
		{Name: "s_ab", DSL: "a & b"},
	}
	r := newTestMatrix(t, baseMatrixConfig(), sets, nil, map[string]process.Process{"a": a, "b": b})

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

	sets := config.OrderedSets{
		{Name: "s_ab", DSL: "a & b"},
	}
	r := newTestMatrix(t, baseMatrixConfig(), sets, nil, map[string]process.Process{"a": a, "b": pb})

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
// completes, and then evicts it. This exercises the scheduler folding in-flight
// swap targets into the running set it hands the swapper.
func TestMatrix_IncompatibleQueues(t *testing.T) {
	a := newFakeProcess("a")
	pb := newFakeProcess("b")

	sets := config.OrderedSets{
		{Name: "s_a", DSL: "a"},
		{Name: "s_b", DSL: "b"},
	}
	r := newTestMatrix(t, baseMatrixConfig(), sets, nil, map[string]process.Process{"a": a, "b": pb})

	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		r.ServeHTTP(w1, newRequest("a"))
		close(done1)
	}()
	waitProcessed(t, r.testProcessed, 1)

	// B arrives before A transitions to StateStarting. The running set the
	// scheduler builds includes A (an in-flight swap target), so the solver
	// returns evict=[a] and collidesWith forces B to queue.
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
	s := newTestMatrixSolver(t, config.OrderedSets{
		{Name: "first", DSL: "a & b"},
		{Name: "second", DSL: "a & c"},
	}, nil, "a", "b", "c")

	// No models running, request "a": both sets have cost 0 and contain a.
	// Definition order: "first" wins.
	result := s.Solve("a", nil, nil, nil)
	if result.SetName != "first" {
		t.Errorf("SetName=%q want %q", result.SetName, "first")
	}
}

// TestMatrixSolver_EvictCostsPreferred verifies that higher evict costs steer
// the solver toward a cheaper set.
func TestMatrixSolver_EvictCostsPreferred(t *testing.T) {
	// b is expensive to evict; c is cheap. Request "a" with both b and c
	// running. The solver should pick the set that keeps b.
	s := newTestMatrixSolver(t, config.OrderedSets{
		{Name: "a_with_c", DSL: "a & c"}, // would evict b (cost 10)
		{Name: "a_with_b", DSL: "a & b"}, // would evict c (cost 1)
	}, map[string]int{"b": 10, "c": 1}, "a", "b", "c")

	result := s.Solve("a", []string{"b", "c"}, nil, nil)
	if result.SetName != "a_with_b" {
		t.Errorf("SetName=%q want %q (keep expensive b)", result.SetName, "a_with_b")
	}
	if len(result.Evict) != 1 || result.Evict[0] != "c" {
		t.Errorf("Evict=%v want [c]", result.Evict)
	}
}

func newTestMatrixSolver(t *testing.T, sets config.OrderedSets, evictCosts map[string]int, modelNames ...string) *matrixSolver {
	t.Helper()
	models := make(map[string]config.ModelConfig, len(modelNames))
	for _, model := range modelNames {
		models[model] = config.ModelConfig{}
	}
	matrix := &config.MatrixConfig{
		EvictCosts: evictCosts,
		Sets:       sets,
	}
	if err := config.ValidateMatrix(matrix, models); err != nil {
		t.Fatalf("ValidateMatrix: %v", err)
	}
	tracker := newLoadCostTracker(matrix.ResolvedEvictCosts(), logmon.NewWriter(io.Discard))
	return newMatrixSolver(matrix.Program(), tracker.EvictCosts, config.ReclaimMinimal)
}

// TestMatrixSwapper_ReclaimQueueProtectsQueued verifies that queue reclaim
// keeps the evict list at the minimal count (the budget-2 set drops two of
// the three running models) while steering the choice away from a model the
// queue still references; that the decision is not cached while the queue is
// non-empty; and that an empty queue falls back to the cacheable minimal
// behaviour.
func TestMatrixSwapper_ReclaimQueueProtectsQueued(t *testing.T) {
	models := map[string]config.ModelConfig{"t": {}, "a": {}, "b": {}, "c": {}, "d": {}}
	matrix := &config.MatrixConfig{
		Reclaim: config.ReclaimQueue,
		Sets: config.OrderedSets{
			{Name: "pool", DSL: "(t | a | b | c | d)"},
			{Name: "all", DSL: "+pool & +pool"},
		},
	}
	if err := config.ValidateMatrix(matrix, models); err != nil {
		t.Fatalf("ValidateMatrix: %v", err)
	}
	tracker := newLoadCostTracker(matrix.ResolvedEvictCosts(), logmon.NewWriter(io.Discard))
	sw := &matrixSwapper{
		solver: newMatrixSolver(matrix.Program(), tracker.EvictCosts, config.ReclaimQueue),
	}

	// Budget 2: with a, b, c running and d queued (not running), the minimal
	// set keeps the target plus one of a, b, c and evicts two. The queue does
	// not widen the list.
	evict := sw.EvictionFor("t", []string{"a", "b", "c"}, []string{"d"}, nil)
	if len(evict) != 2 {
		t.Fatalf("Evict=%v want exactly two evictions (minimal count, no extension)", evict)
	}
	if sw.lastValid {
		t.Fatal("queue-reclaim decisions with a non-empty queue must not be cached")
	}

	// The queue changes: now a is queued and running, so a is protected and
	// the two idle unqueued models turn over. The next decision must follow
	// (no stale cache).
	evict = sw.EvictionFor("t", []string{"a", "b", "c"}, []string{"a"}, nil)
	if len(evict) != 2 {
		t.Fatalf("Evict=%v want two evictions", evict)
	}
	for _, m := range evict {
		if m == "a" {
			t.Fatalf("Evict=%v must not evict queued model a", evict)
		}
	}

	// Empty queue: minimal behaviour (the budget-2 set drops two of the three
	// running models) and the cache engages.
	evict = sw.EvictionFor("t", []string{"a", "b", "c"}, nil, nil)
	if len(evict) != 2 {
		t.Fatalf("Evict=%v want exactly two evictions (empty queue is minimal)", evict)
	}
	if !sw.lastValid {
		t.Fatal("queue-reclaim decisions with an empty queue should be cached")
	}
}

// TestMatrix_BurstDrainsIdleSlots encodes the load-test acceptance criterion:
// with a full fleet and a burst of six simultaneous requests, five models
// start in parallel with disjoint evictions and one request queues. The
// moment the first loaded model goes idle (ready and its request served),
// the queued request evicts that idle model and starts — the four still
// loading are left alone: loading is fine, idle is not.
func TestMatrix_BurstDrainsIdleSlots(t *testing.T) {
	// Five ready residents, six burst targets that stay "loading" until the
	// test marks them ready.
	var residents []*fakeProcess
	burst := map[string]*fakeProcess{}
	processes := map[string]process.Process{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"} {
		p := newFakeProcess(id)
		processes[id] = p
		switch {
		case id >= "f":
			burst[id] = p
		default:
			p.markReady()
			go p.Run(0) // park so Stop has something to release
			residents = append(residents, p)
		}
	}

	sets := config.OrderedSets{
		{Name: "pool", DSL: "(a|b|c|d|e|f|g|h|i|j|k)"},
		// Budget 5.
		{Name: "all", DSL: "+pool & +pool & +pool & +pool & +pool"},
	}
	conf := baseMatrixConfig()
	conf.Matrix.Sets = sets
	models := make(map[string]config.ModelConfig, len(processes))
	for id := range processes {
		models[id] = config.ModelConfig{}
	}
	if err := config.ValidateMatrix(conf.Matrix, models); err != nil {
		t.Fatalf("ValidateMatrix: %v", err)
	}
	var logBuf bytes.Buffer
	logger := logmon.NewWriter(&logBuf)
	tracker := newLoadCostTracker(conf.Matrix.ResolvedEvictCosts(), logger)
	swapper := &matrixSwapper{
		solver: newMatrixSolver(conf.Matrix.Program(), tracker.EvictCosts, config.ReclaimQueue),
		logger: logger,
	}
	base, err := newBaseRouter("matrix", conf, processes, logger, swapper)
	if err != nil {
		t.Fatalf("newBaseRouter: %v", err)
	}
	base.testProcessed = make(chan struct{}, 64)
	r := &Matrix{baseRouter: base}
	go base.run()
	t.Cleanup(func() {
		if !r.shuttingDown.Load() {
			_ = r.Shutdown(time.Second)
		}
	})

	// Fire the burst: six simultaneous requests, one per target. Each call
	// blocks until its model is ready and served; the models the test never
	// marks ready only answer when Shutdown cancels them, so the goroutines
	// are fire-and-forget.
	for _, id := range []string{"f", "g", "h", "i", "j", "k"} {
		go func(id string) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, newRequest(id))
		}(id)
	}

	// Wait until five of the six targets have started loading.
	var started []string
	deadline := time.Now().Add(10 * time.Second)
	for {
		started = nil
		for _, id := range []string{"f", "g", "h", "i", "j", "k"} {
			if burst[id].runCalls.Load() >= 1 {
				started = append(started, id)
			}
		}
		if len(started) == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/6 burst targets started: %v", len(started), started)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The five starts must have evicted five distinct residents — the
	// evictions are disjoint, so five swaps ran in parallel instead of one
	// serial chain.
	stopped := 0
	for _, p := range residents {
		if p.stopCalls.Load() == 1 {
			stopped++
		}
	}
	if stopped != 5 {
		t.Fatalf("only %d/5 residents evicted (disjoint evictions expected): %v",
			stopped, stopsOf(residents))
	}
	var queued string
	for _, id := range []string{"f", "g", "h", "i", "j", "k"} {
		if burst[id].runCalls.Load() == 0 {
			queued = id
		}
	}
	if queued == "" {
		t.Fatal("all six burst targets started; with a budget of five one must queue")
	}

	// The first loaded model goes idle: ready, its request served. The
	// queued request must now evict exactly that idle model and start —
	// the four still loading are protected (they are loading, not idle).
	first := burst[started[0]]
	first.markReady()

	deadline = time.Now().Add(10 * time.Second)
	for {
		if burst[queued].runCalls.Load() >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued %s never started after %s went idle", queued, started[0])
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The idle model is turned over; the loading models are not evicted.
	if first.stopCalls.Load() != 1 {
		t.Fatalf("idle %s not evicted for queued %s (stopCalls=%d)", started[0], queued, first.stopCalls.Load())
	}
	for _, id := range started[1:] {
		if got := burst[id].stopCalls.Load(); got != 0 {
			t.Fatalf("loading model %s evicted (stopCalls=%d); only idle models may turn over", id, got)
		}
	}
}

func stopsOf(ps []*fakeProcess) []string {
	var out []string
	for _, p := range ps {
		out = append(out, fmt.Sprintf("%s=%d", p.id, p.stopCalls.Load()))
	}
	return out
}
