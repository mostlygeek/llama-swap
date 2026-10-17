package router

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/stretchr/testify/require"
)

func newCapacityPlanner(total int64, usage map[string]int64, costs map[string]int64) *capacitySwapper {
	models := make(map[string]config.ModelConfig, len(usage))
	for id, amount := range usage {
		cost := int64(1)
		if value, ok := costs[id]; ok {
			cost = value
		}
		models[id] = config.ModelConfig{Capacity: &config.ModelCapacityConfig{Usage: amount, EvictCost: cost}}
	}
	return &capacitySwapper{total: total, models: models, logger: logmon.NewWriter(io.Discard)}
}

func TestCapacity_EvictionFor(t *testing.T) {
	for _, tc := range []struct {
		name          string
		total         int64
		usage, costs  map[string]int64
		running, want []string
	}{
		{"empty", 10, map[string]int64{"x": 10}, nil, nil, nil},
		{"below total regardless of cost", 10, map[string]int64{"a": 2, "x": 3}, map[string]int64{"a": 1, "x": 100}, []string{"a"}, nil},
		{"exact total", 10, map[string]int64{"a": 4, "x": 6}, nil, []string{"a"}, nil},
		{"already resident", 10, map[string]int64{"a": 4, "x": 6}, nil, []string{"x", "a"}, nil},
		{"single", 10, map[string]int64{"a": 8, "x": 6}, nil, []string{"a"}, []string{"a"}},
		{"multiple", 10, map[string]int64{"a": 4, "b": 4, "x": 9}, nil, []string{"b", "a"}, []string{"a", "b"}},
		// A alone cannot free enough capacity. B alone is the cheapest solution.
		{"complete solution", 10, map[string]int64{"a": 2, "b": 8, "x": 7}, map[string]int64{"a": 100, "b": 1}, []string{"a", "b"}, []string{"b"}},
		{"retain expensive model", 10, map[string]int64{"a": 4, "b": 6, "x": 4}, map[string]int64{"a": 1, "b": 10}, []string{"a", "b"}, []string{"a"}},
		{"cheapest requires multiple models", 20, map[string]int64{"a": 10, "b": 5, "c": 5, "x": 10}, map[string]int64{"a": 100, "b": 1, "c": 1}, []string{"a", "b", "c"}, []string{"b", "c"}},
		{"equal cost prefers fewer models", 20, map[string]int64{"a": 5, "b": 5, "c": 10, "x": 10}, map[string]int64{"a": 1, "b": 1, "c": 2}, []string{"a", "b", "c"}, []string{"c"}},
		{"tie IDs", 10, map[string]int64{"a": 5, "b": 5, "x": 5}, nil, []string{"b", "a"}, []string{"a"}},
		{"large cost", 10, map[string]int64{"a": 5, "b": 5, "x": 5}, map[string]int64{"b": 9223372036854775805}, []string{"a", "b"}, []string{"a"}},
		{"maximum total", 9223372036854775807, map[string]int64{"a": 9223372036854775806, "x": 1}, nil, []string{"a"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newCapacityPlanner(tc.total, tc.usage, tc.costs)
			original := slices.Clone(tc.running)
			require.Equal(t, tc.want, s.EvictionFor("x", tc.running))
			require.Equal(t, original, tc.running, "must not mutate scheduler snapshot")
			slices.Reverse(tc.running)
			require.Equal(t, tc.want, s.EvictionFor("x", tc.running))
		})
	}
}

func TestCapacity_DecisionCache(t *testing.T) {
	s := newCapacityPlanner(10, map[string]int64{"a": 6, "b": 4, "x": 6, "y": 4}, map[string]int64{"a": 2, "x": 2})
	var logs bytes.Buffer
	s.logger = logmon.NewWriter(&logs)
	running := []string{"a", "b"}
	evict := s.EvictionFor("x", running)
	require.Equal(t, []string{"a"}, evict)

	// An equal snapshot must reuse the search result without allocating.
	snapshot := slices.Clone(running)
	require.Zero(t, testing.AllocsPerRun(10, func() {
		s.EvictionFor("x", snapshot)
	}))
	s.OnSwapStart("x", snapshot)
	require.Contains(t, logs.String(), "capacity: model=x evict=[a] total=10")
	require.Same(t, &evict[0], &s.lastEvict[0])

	// A different target needs a new decision even with the same residents.
	require.Equal(t, []string{"b"}, s.EvictionFor("y", running))

	// The scheduler can reuse its input slice. The cache must own its snapshot.
	running[1] = "x"
	require.Equal(t, []string{"a", "b"}, s.lastRunning)
	require.Equal(t, []string{"a"}, s.EvictionFor("y", running))

	// Cache empty decisions too; logging must not repeat their search.
	running = []string{"b"}
	require.Empty(t, s.EvictionFor("y", running))
	require.Zero(t, testing.AllocsPerRun(10, func() {
		s.OnSwapStart("y", running)
	}))
	// An empty decision must not hide a later change in residents.
	require.Equal(t, []string{"a"}, s.EvictionFor("y", []string{"a", "x"}))
}

// Compare the pruned search to an independent exhaustive oracle on small
// random budgets, including total cost, eviction count and model-ID ties.
func TestCapacity_ExhaustiveOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(1004))
	for trial := 0; trial < 500; trial++ {
		usage := map[string]int64{"x": int64(1 + rng.Intn(30))}
		costs := map[string]int64{}
		var running []string
		var used int64
		for i := 0; i < 8; i++ {
			id := fmt.Sprintf("m%d", i)
			amount := int64(1 + rng.Intn(8))
			if used+amount > 30 {
				continue
			}
			used += amount
			running = append(running, id)
			usage[id] = amount
			costs[id] = int64(1 + rng.Intn(7))
		}
		var want []string
		var bestCost int64
		found := false
		for mask := 0; mask < 1<<len(running); mask++ {
			var evict []string
			var cost int64
			var freed int64
			for i, id := range running {
				if mask&(1<<i) != 0 {
					evict = append(evict, id)
					cost += costs[id]
					freed += usage[id]
				}
			}
			if used-freed+usage["x"] > 30 {
				continue
			}
			if !found || cost < bestCost || cost == bestCost && (len(evict) < len(want) || len(evict) == len(want) && slices.Compare(evict, want) < 0) {
				want, bestCost, found = evict, cost, true
			}
		}
		s := newCapacityPlanner(30, usage, costs)
		require.Equal(t, want, s.EvictionFor("x", running), "trial %d", trial)
	}
}

func TestCapacity_UnloadAndRestart(t *testing.T) {
	a, b, c := newFakeProcess("a"), newFakeProcess("b"), newFakeProcess("c")
	a.autoReady, b.autoReady, c.autoReady = true, true, true
	r := newTestBase(t, map[string]process.Process{"a": a, "b": b, "c": c}, newCapacityPlanner(10, map[string]int64{"a": 4, "b": 6, "c": 6}, map[string]int64{"a": 100}))
	for _, id := range []string{"a", "b", "a", "c"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, newRequest(id))
		require.Equal(t, 200, w.Code)
	}
	require.Equal(t, process.StateReady, a.State())
	require.Equal(t, int32(0), a.stopCalls.Load())
	require.Equal(t, process.StateStopped, b.State())
	require.Equal(t, process.StateReady, c.State())
	r.Unload(0, "c")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, newRequest("b"))
	require.Equal(t, 200, w.Code)
	require.Equal(t, process.StateReady, b.State())
	require.Equal(t, int32(0), a.stopCalls.Load())
}

func BenchmarkCapacity_EvictionFor(b *testing.B) {
	for _, n := range []int{8, 16, 24} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			usage := map[string]int64{"x": int64(n / 2)}
			var running []string
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("m%02d", i)
				running = append(running, id)
				usage[id] = 1
			}
			s := newCapacityPlanner(int64(n), usage, nil)
			b.ReportAllocs()
			for b.Loop() {
				// Measure the search rather than repeated cache hits.
				s.evictionFor("x", running)
			}
		})
	}
}

// Three requests arrive while the first two starts are pending. The third
// cannot consume unreserved capacity, or stop a chosen victim mid-request.
func TestCapacity_PendingStartsAndBusyEviction(t *testing.T) {
	a, b, c := newFakeProcess("a"), newFakeProcess("b"), newFakeProcess("c")
	a.serveBlock = make(chan struct{})
	c.autoReady = true
	r := newTestBase(t, map[string]process.Process{"a": a, "b": b, "c": c}, newCapacityPlanner(10, map[string]int64{"a": 4, "b": 4, "c": 4}, nil))
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(a.serveBlock) }) })
	request := func(id string) <-chan int {
		done := make(chan int, 1)
		go func() { w := httptest.NewRecorder(); r.ServeHTTP(w, newRequest(id)); done <- w.Code }()
		return done
	}
	adone := request("a")
	waitProcessed(t, r.testProcessed, 1)
	waitSignal(t, a.runStarted, "a start")
	bdone := request("b")
	waitProcessed(t, r.testProcessed, 1)
	waitSignal(t, b.runStarted, "b start")
	cdone := request("c")
	waitProcessed(t, r.testProcessed, 1)
	require.Zero(t, c.runCalls.Load(), "pending starts consume capacity")
	a.markReady()
	waitProcessed(t, r.testProcessed, 1)
	waitSignal(t, a.serveStarted, "a serving")
	b.markReady()
	waitProcessed(t, r.testProcessed, 1)
	require.Zero(t, a.stopCalls.Load(), "busy victim must finish before eviction")
	require.Zero(t, c.runCalls.Load())
	release.Do(func() { close(a.serveBlock) })
	for _, done := range []<-chan int{adone, bdone, cdone} {
		select {
		case code := <-done:
			require.Equal(t, 200, code)
		case <-t.Context().Done():
			t.Fatal("request did not complete")
		}
	}
	require.Equal(t, process.StateStopped, a.State())
	require.Equal(t, process.StateReady, b.State())
	require.Equal(t, process.StateReady, c.State())
	require.False(t, a.stoppedWhileServing.Load())
}

func TestCapacity_NewRejectsInvalidBudget(t *testing.T) {
	_, err := NewCapacity(config.Config{}, logmon.NewGroup(io.Discard, true, true, true))
	require.ErrorContains(t, err, "capacity configuration is required")
}
