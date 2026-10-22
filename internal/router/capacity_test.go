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

// newCapacityPlanner builds a single-pool planner for focused scheduling tests.
func newCapacityPlanner(total int64, usage map[string]int64, costs map[string]int64) *capacitySwapper {
	models := make(map[string]config.ModelConfig, len(usage))
	for id, amount := range usage {
		models[id] = config.ModelConfig{Capacities: map[string]int64{"ram": amount}, EvictCost: costs[id]}
	}
	return &capacitySwapper{capacity: &config.CapacityConfig{Pools: map[string]int64{"ram": total}}, models: models, logger: logmon.NewWriter(io.Discard)}
}

// TestCapacity_EvictionFor checks feasibility, manual costs, and deterministic
// ties without allowing the planner to mutate the scheduler's snapshot.
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
		{"zero cost prefers fewer models", 20, map[string]int64{"a": 5, "b": 5, "c": 10, "x": 10}, nil, []string{"a", "b", "c"}, []string{"c"}},
		{"retain utility with mandatory large victim", 110, map[string]int64{"a": 1, "b": 100, "x": 50}, nil, []string{"a", "b"}, []string{"b"}},
		{"manual workload preference", 110, map[string]int64{"utility": 1, "llm": 50, "embeddings": 50, "x": 50}, map[string]int64{"llm": 50, "embeddings": 10}, []string{"utility", "llm", "embeddings"}, []string{"embeddings"}},
		{"zero usage resident", 10, map[string]int64{"a": 0, "b": 10, "x": 10}, nil, []string{"a", "b"}, []string{"b"}},
		{"zero usage target", 10, map[string]int64{"a": 10, "x": 0}, nil, []string{"a"}, nil},
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

// TestCapacity_DecisionCache verifies decision reuse for logging and invalidation
// when either the target or the resident snapshot changes.
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
	require.Contains(t, logs.String(), "capacity: model=x evict=[a] pools=map[ram:10]")
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

// TestCapacity_UnloadAndRestart checks that eviction and explicit unload release
// capacity while retained models continue serving without restarting.
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

// BenchmarkCapacity_EvictionFor measures uncached planning as residency grows.
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

// TestCapacity_PendingStartsAndBusyEviction checks that pending starts reserve
// capacity and that eviction waits for a chosen victim's active request to end.
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

// TestCapacity_NewRejectsInvalidBudget checks constructor validation for callers
// that supply configuration directly instead of using the config loader.
func TestCapacity_NewRejectsInvalidBudget(t *testing.T) {
	_, err := NewCapacity(config.Config{}, logmon.NewGroup(io.Discard, true, true, true))
	require.ErrorContains(t, err, "capacity configuration is required")
}

// TestCapacity_Pools checks independent and overlapping resource constraints,
// counting each model's eviction cost once across all pools it consumes.
func TestCapacity_Pools(t *testing.T) {
	model := func(cost, ram, gpu0, gpu1 int64) config.ModelConfig {
		return config.ModelConfig{EvictCost: cost, Capacities: map[string]int64{"ram": ram, "gpu0": gpu0, "gpu1": gpu1}}
	}
	for _, tc := range []struct {
		name          string
		models        map[string]config.ModelConfig
		running, want []string
	}{
		{"disjoint", map[string]config.ModelConfig{"a": model(0, 0, 10, 0), "x": model(0, 0, 0, 10)}, []string{"a"}, nil},
		{"shared cost counted once", map[string]config.ModelConfig{"a": model(3, 5, 5, 0), "b": model(2, 5, 0, 0), "c": model(2, 0, 5, 0), "x": model(0, 5, 5, 0)}, []string{"c", "a", "b"}, []string{"a"}},
		{"independent deficits", map[string]config.ModelConfig{"a": model(0, 10, 0, 0), "b": model(0, 0, 10, 0), "c": model(0, 0, 0, 10), "x": model(0, 1, 1, 0)}, []string{"a", "b", "c"}, []string{"a", "b"}},
		{"undeclared target", map[string]config.ModelConfig{"a": model(0, 10, 10, 10), "x": {}}, []string{"a"}, nil},
		{"undeclared resident", map[string]config.ModelConfig{"a": {}, "x": model(0, 10, 10, 10)}, []string{"a"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &capacitySwapper{capacity: &config.CapacityConfig{Pools: map[string]int64{"ram": 10, "gpu0": 10, "gpu1": 10}}, models: tc.models}
			require.NoError(t, config.ValidateCapacity(s.capacity, tc.models))
			require.Equal(t, tc.want, s.EvictionFor("x", tc.running))
		})
	}
}

// TestCapacity_ExhaustiveOracle compares planning with independent subset
// enumeration and checks feasible, irredundant results under search cutoffs.
func TestCapacity_ExhaustiveOracle(t *testing.T) {
	for _, poolCount := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d pools", poolCount), func(t *testing.T) {
			rng := rand.New(rand.NewSource(319))
			pools := map[string]int64{}
			for p := 0; p < poolCount; p++ {
				pools[fmt.Sprint(p)] = 10
			}
			for trial := 0; trial < 400; trial++ {
				models := map[string]config.ModelConfig{}
				var running []string
				for i := 0; i < 9; i++ {
					id := fmt.Sprintf("m%d", i)
					if i == 8 {
						id = "x"
					} else {
						running = append(running, id)
					}
					amounts := map[string]int64{}
					for p := 0; p < poolCount; p++ {
						amounts[fmt.Sprint(p)] = int64(rng.Intn(5))
					}
					models[id] = config.ModelConfig{EvictCost: int64(rng.Intn(7)), Capacities: amounts}
					if trial%3 == 0 {
						model := models[id]
						model.EvictCost = 0
						models[id] = model
					}
				}
				s := &capacitySwapper{capacity: &config.CapacityConfig{Pools: pools}, models: models}
				require.NoError(t, config.ValidateCapacity(s.capacity, models))
				fits := func(evict []string) bool {
					for pool, total := range s.capacity.Pools {
						used := models["x"].Capacities[pool]
						for _, id := range running {
							if !slices.Contains(evict, id) {
								used += models[id].Capacities[pool]
							}
						}
						if used > total {
							return false
						}
					}
					return true
				}
				var want []string
				var bestCost int64
				found := false
				for mask := 0; mask < 1<<len(running); mask++ {
					var evict []string
					var cost int64
					for i, id := range running {
						if mask&(1<<i) != 0 {
							evict = append(evict, id)
							cost += models[id].EvictCost
						}
					}
					if !fits(evict) {
						continue
					}
					if !found || cost < bestCost || cost == bestCost && (len(evict) < len(want) || len(evict) == len(want) && slices.Compare(evict, want) < 0) {
						want, bestCost, found = evict, cost, true
					}
				}
				require.Equal(t, want, s.EvictionFor("x", running), "trial %d", trial)
				require.False(t, s.searchLimited)
				// Exhausting the node budget still returns a feasible, deterministic
				// plan with no victim that could simply have been retained.
				for _, limit := range []int{1, 3, 17} {
					s.searchLimit = limit
					s.lastValid = false
					actual := s.EvictionFor("x", running)
					require.True(t, fits(actual), "fallback trial %d", trial)
					require.LessOrEqual(t, s.searchNodes, limit)
					for i := range actual {
						without := append(slices.Clone(actual[:i]), actual[i+1:]...)
						require.False(t, fits(without), "redundant fallback victim in trial %d", trial)
					}
					s.lastValid = false
					slices.Reverse(running)
					require.Equal(t, actual, s.EvictionFor("x", running))
				}
			}
		})
	}
}

// TestCapacity_SearchCutoffRetainsRedundantVictim checks that a cheaper plan
// found before a cutoff does not evict a model that frees no needed resources.
func TestCapacity_SearchCutoffRetainsRedundantVictim(t *testing.T) {
	s := newCapacityPlanner(10, map[string]int64{"a": 0, "b": 6, "c": 2, "d": 2, "x": 4}, map[string]int64{"b": 6, "c": 4, "d": 4})
	// The greedy seed evicts C+D (cost 8). DFS first finds A+B (cost 6)
	// before the cutoff. A contributes nothing and must still be retained.
	s.searchLimit = 3
	require.Equal(t, []string{"b"}, s.EvictionFor("x", []string{"a", "b", "c", "d"}))
	require.True(t, s.searchLimited)
}
