package proxy

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to build expanded sets for solver tests
func makeExpandedSets(sets ...struct {
	name   string
	models []string
}) []config.ExpandedSet {
	var result []config.ExpandedSet
	for _, s := range sets {
		result = append(result, config.ExpandedSet{
			SetName: s.name,
			Models:  s.models,
		})
	}
	return result
}

func es(name string, models ...string) struct {
	name   string
	models []string
} {
	return struct {
		name   string
		models []string
	}{name, models}
}

func TestMatrixSolver_AlreadyRunning(t *testing.T) {
	solver := NewMatrixSolver(
		makeExpandedSets(es("s1", "a", "b")),
		nil,
	)

	result, err := solver.Solve("a", []string{"a"})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"a"}, result.TargetSet)
	assert.Equal(t, "s1", result.SetName)
}

func TestMatrixSolver_NotInAnySet_RunsAlone(t *testing.T) {
	solver := NewMatrixSolver(
		makeExpandedSets(es("s1", "a", "b")),
		nil,
	)

	// Model "c" not in any set
	result, err := solver.Solve("c", []string{"a", "b"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, result.Evict)
	assert.Equal(t, []string{"c"}, result.TargetSet)
}

func TestMatrixSolver_NotInAnySet_NothingRunning(t *testing.T) {
	solver := NewMatrixSolver(
		makeExpandedSets(es("s1", "a", "b")),
		nil,
	)

	result, err := solver.Solve("c", []string{})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"c"}, result.TargetSet)
}

func TestMatrixSolver_SingleSet_EvictsNonMembers(t *testing.T) {
	// Set: [a, b]. Request a when b and c are running.
	solver := NewMatrixSolver(
		makeExpandedSets(es("s1", "a", "b")),
		nil,
	)

	result, err := solver.Solve("a", []string{"b", "c"})
	require.NoError(t, err)
	// c is not in the set, so it gets evicted. b is in the set, so it stays.
	assert.Equal(t, []string{"c"}, result.Evict)
	assert.Equal(t, []string{"a", "b"}, result.TargetSet)
}

func TestMatrixSolver_PicksLowestCost(t *testing.T) {
	// Two sets containing model "a":
	// s1: [a, v]  — if v is running, cost=0; if L is running, cost=30
	// s2: [a, L]  — if L is running, cost=0; if v is running, cost=50
	solver := NewMatrixSolver(
		makeExpandedSets(
			es("s1", "a", "v"),
			es("s2", "a", "L"),
		),
		map[string]int{"v": 50, "L": 30},
	)

	// v is running. Switching to a:
	// s1 cost: v is in s1, so 0
	// s2 cost: v is NOT in s2, so 50
	// => pick s1
	result, err := solver.Solve("a", []string{"v"})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"a", "v"}, result.TargetSet)

	// L is running. Switching to a:
	// s1 cost: L is NOT in s1, so 30
	// s2 cost: L is in s2, so 0
	// => pick s2
	result, err = solver.Solve("a", []string{"L"})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"a", "L"}, result.TargetSet)
}

func TestMatrixSolver_TieBreakingByDefinitionOrder(t *testing.T) {
	// Two sets with identical cost. Definition order should win.
	solver := NewMatrixSolver(
		makeExpandedSets(
			es("s1", "a", "x"),
			es("s2", "a", "y"),
		),
		nil,
	)

	// Nothing running, both sets cost 0. s1 is first.
	result, err := solver.Solve("a", []string{})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"a", "x"}, result.TargetSet)
}

func TestMatrixSolver_EvictCostPreservesExpensive(t *testing.T) {
	// Model "v" costs 50 to evict, "m" costs 1 (default).
	// Sets: [g,v], [g,m]
	// Running: v, m. Request g.
	// s1=[g,v]: evict m (cost 1), keep v
	// s2=[g,m]: evict v (cost 50), keep m
	// => pick s1
	solver := NewMatrixSolver(
		makeExpandedSets(
			es("s1", "g", "v"),
			es("s2", "g", "m"),
		),
		map[string]int{"v": 50},
	)

	result, err := solver.Solve("g", []string{"v", "m"})
	require.NoError(t, err)
	assert.Equal(t, []string{"m"}, result.Evict)
	assert.Equal(t, []string{"g", "v"}, result.TargetSet)
}

func TestMatrixSolver_NothingRunning(t *testing.T) {
	solver := NewMatrixSolver(
		makeExpandedSets(
			es("s1", "g", "v"),
			es("s2", "q", "v"),
		),
		nil,
	)

	result, err := solver.Solve("g", []string{})
	require.NoError(t, err)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"g", "v"}, result.TargetSet)
}

// TestMatrix_ProxyRequestSwapRaceAgainstFastPath verifies that an eviction
// cannot stop a process while an in-flight ProxyRequest for that process is
// still in the [m.Unlock, Process.inFlightRequests.Add(1)] window. Without
// matrix-level inflight tracking, the eviction's Stop() races with the
// pending request and kills it mid-start.
func TestMatrix_ProxyRequestSwapRaceAgainstFastPath(t *testing.T) {
	cfg := config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
			"model2": getTestSimpleResponderConfig("model2"),
		},
		ExpandedSets: []config.ExpandedSet{
			{SetName: "s1", Models: []string{"model1"}},
			{SetName: "s2", Models: []string{"model2"}},
		},
		Matrix: &config.MatrixConfig{},
	}

	m := NewMatrix(cfg, testLogger, testLogger)
	defer m.StopProcesses(StopImmediately)

	// Bypass real subprocesses so the test is fast and deterministic.
	m.processes["model1"].testHandler = newTestHandler("model1")
	m.processes["model2"].testHandler = newTestHandler("model2")

	// Prime: run a request through model1 so it reaches StateReady and
	// subsequent requests take the no-eviction path.
	primeReq := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	primeW := httptest.NewRecorder()
	require.NoError(t, m.ProxyRequest("model1", primeW, primeReq))
	require.Equal(t, http.StatusOK, primeW.Code)
	require.Equal(t, StateReady, m.processes["model1"].CurrentState())
	require.Equal(t, StateStopped, m.processes["model2"].CurrentState())

	// Install fast-path hook that signals arrival and waits for release.
	// This parks R2 at the race window — after m.Lock is released but
	// before Process.inFlightRequests.Add(1).
	r2Reached := make(chan struct{})
	r2Release := make(chan struct{})
	m.testDelayFastPath = func() {
		close(r2Reached)
		<-r2Release
	}

	// R2: no-eviction request for model1. Will pause at the hook.
	r2Done := make(chan struct{})
	w2 := httptest.NewRecorder()
	go func() {
		defer close(r2Done)
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		assert.NoError(t, m.ProxyRequest("model1", w2, req))
	}()

	// Deterministically wait for R2 to reach the race window.
	<-r2Reached

	// R3: request for model2 which requires evicting model1. Must wait for
	// R2 to finish before touching model1.
	r3Done := make(chan struct{})
	w3 := httptest.NewRecorder()
	go func() {
		defer close(r3Done)
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		assert.NoError(t, m.ProxyRequest("model2", w3, req))
	}()

	// Spin until R3 has acquired m.Lock and entered the eviction path. In
	// the fixed code, R3 then blocks on m.inflight.Wait() while still
	// holding the lock, so TryLock keeps failing.
	for m.TryLock() {
		m.Unlock()
		runtime.Gosched()
	}

	// Bounded poll: give R3 a chance to demonstrate the bug by mutating
	// state. In the fixed code R3 is blocked and nothing changes; in the
	// buggy code R3 will Stop() model1 and start model2 within microseconds.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if m.processes["model1"].CurrentState() != StateReady ||
			m.processes["model2"].CurrentState() != StateStopped {
			break
		}
		done := false
		select {
		case <-r3Done:
			done = true
		default:
		}
		if done {
			break
		}
		runtime.Gosched()
	}

	// Invariant: R3 must be blocked while R2 is still in flight.
	select {
	case <-r3Done:
		t.Fatal("eviction completed while in-flight request was still pending — race not prevented")
	default:
	}
	assert.Equal(t, StateReady, m.processes["model1"].CurrentState(),
		"model1 must stay Ready while an in-flight request is pending")
	assert.Equal(t, StateStopped, m.processes["model2"].CurrentState(),
		"model2 must not be started until R2 finishes and model1 is evicted")

	// Release R2 and let both requests finish.
	close(r2Release)
	<-r2Done
	<-r3Done

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), "model1")
	assert.Equal(t, http.StatusOK, w3.Code)
	assert.Contains(t, w3.Body.String(), "model2")
}

func TestMatrixSolver_FullScenario(t *testing.T) {
	// Simulates the example config:
	// standard: [g,v], [q,v], [m,v]
	// with_rerank: [g,v,e], [q,v,e]
	// creative: [g,sd], [q,sd]
	// full: [L]
	solver := NewMatrixSolver(
		makeExpandedSets(
			es("standard", "g", "v"),
			es("standard", "q", "v"),
			es("standard", "m", "v"),
			es("with_rerank", "e", "g", "v"),
			es("with_rerank", "e", "q", "v"),
			es("creative", "g", "sd"),
			es("creative", "q", "sd"),
			es("full", "L"),
		),
		map[string]int{"v": 50, "L": 30, "whisper": 10},
	)

	// Running: g, v. Request q.
	// standard[q,v]: evict g (cost 1), keep v. Total: 1.
	// with_rerank[q,v,e]: evict g (cost 1), keep v. Total: 1.
	// => tie, pick first by definition order = standard[q,v]
	result, err := solver.Solve("q", []string{"g", "v"})
	require.NoError(t, err)
	assert.Equal(t, []string{"g"}, result.Evict)
	assert.Equal(t, []string{"q", "v"}, result.TargetSet)

	// Running: g, v. Request L.
	// full[L]: evict g (cost 1) + v (cost 50). Total: 51.
	// Only one set contains L, so pick it.
	result, err = solver.Solve("L", []string{"g", "v"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"g", "v"}, result.Evict)
	assert.Equal(t, []string{"L"}, result.TargetSet)

	// Running: g, v. Request sd.
	// creative[g,sd]: evict v (cost 50). Total: 50.
	// creative[q,sd]: evict g (cost 1) + v (cost 50). Total: 51.
	// => pick creative[g,sd]
	result, err = solver.Solve("sd", []string{"g", "v"})
	require.NoError(t, err)
	assert.Equal(t, []string{"v"}, result.Evict)
	assert.Equal(t, []string{"g", "sd"}, result.TargetSet)

	// Running: q, v, e. Request g.
	// standard[g,v]: evict q (1) + e (1). Total: 2.
	// with_rerank[g,v,e]: evict q (1). Total: 1.
	// creative[g,sd]: evict q (1) + v (50) + e (1). Total: 52.
	// => pick with_rerank[g,v,e]
	result, err = solver.Solve("g", []string{"e", "q", "v"})
	require.NoError(t, err)
	assert.Equal(t, []string{"q"}, result.Evict)
	assert.Equal(t, []string{"e", "g", "v"}, result.TargetSet)
}
