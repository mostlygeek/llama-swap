package router

import (
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

func newCapacityPlanner(total int64, usage map[string]int64, priorities map[string]int64) *capacitySwapper {
	models := make(map[string]config.ModelConfig, len(usage))
	for id, amount := range usage {
		models[id] = config.ModelConfig{Capacity: &config.ModelCapacityConfig{Usage: amount, EvictionPriority: priorities[id]}}
	}
	return &capacitySwapper{total: total, models: models, logger: logmon.NewWriter(io.Discard)}
}

func TestCapacity_EvictionFor(t *testing.T) {
	for _, tc := range []struct {
		name              string
		total             int64
		usage, priorities map[string]int64
		running, want     []string
	}{
		{"empty", 10, map[string]int64{"x": 10}, nil, nil, nil},
		{"below total", 10, map[string]int64{"a": 2, "x": 3}, nil, []string{"a"}, nil},
		{"exact total", 10, map[string]int64{"a": 4, "x": 6}, nil, []string{"a"}, nil},
		{"already resident", 10, map[string]int64{"a": 4, "x": 6}, nil, []string{"x", "a"}, nil},
		{"single", 10, map[string]int64{"a": 8, "x": 6}, nil, []string{"a"}, []string{"a"}},
		{"multiple", 10, map[string]int64{"a": 4, "b": 4, "x": 9}, nil, []string{"b", "a"}, []string{"a", "b"}},
		// Evicting preferred A first would still require B. B alone suffices.
		{"complete solution", 10, map[string]int64{"a": 2, "b": 8, "x": 7}, map[string]int64{"a": 100, "b": 1}, []string{"a", "b"}, []string{"b"}},
		{"retain lower priority", 10, map[string]int64{"a": 4, "b": 6, "x": 4}, map[string]int64{"a": 1, "b": 10}, []string{"a", "b"}, []string{"b"}},
		{"count before priority", 10, map[string]int64{"a": 5, "b": 2, "c": 3, "x": 5}, map[string]int64{"b": 100, "c": 100}, []string{"a", "b", "c"}, []string{"a"}},
		{"protect lowest across whole set", 12, map[string]int64{"a": 5, "b": 1, "c": 3, "d": 3, "x": 6}, map[string]int64{"b": 100, "c": 40, "d": 40}, []string{"a", "b", "c", "d"}, []string{"c", "d"}},
		{"tie IDs", 10, map[string]int64{"a": 5, "b": 5, "x": 5}, nil, []string{"b", "a"}, []string{"a"}},
		{"maximum priority", 10, map[string]int64{"a": 5, "b": 5, "x": 5}, map[string]int64{"b": 9223372036854775807}, []string{"a", "b"}, []string{"b"}},
		{"maximum total", 9223372036854775807, map[string]int64{"a": 9223372036854775806, "x": 1}, nil, []string{"a"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newCapacityPlanner(tc.total, tc.usage, tc.priorities)
			original := slices.Clone(tc.running)
			require.Equal(t, tc.want, s.EvictionFor("x", tc.running))
			require.Equal(t, original, tc.running, "must not mutate scheduler snapshot")
			slices.Reverse(tc.running)
			require.Equal(t, tc.want, s.EvictionFor("x", tc.running))
		})
	}
}

// Compare the pruned search to an independent exhaustive oracle on small
// random budgets, including count, priority-vector and model-ID ties.
func TestCapacity_ExhaustiveOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(1004))
	for trial := 0; trial < 500; trial++ {
		usage := map[string]int64{"x": int64(1 + rng.Intn(30))}
		priorities := map[string]int64{}
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
			priorities[id] = int64(rng.Intn(7))
		}
		var want []string
		var bestPriorities []int64
		found := false
		for mask := 0; mask < 1<<len(running); mask++ {
			var evict []string
			var values []int64
			var freed int64
			for i, id := range running {
				if mask&(1<<i) != 0 {
					evict = append(evict, id)
					values = append(values, priorities[id])
					freed += usage[id]
				}
			}
			if used-freed+usage["x"] > 30 {
				continue
			}
			slices.Sort(values)
			cmp := slices.Compare(values, bestPriorities)
			if !found || len(evict) < len(want) || len(evict) == len(want) && (cmp > 0 || cmp == 0 && slices.Compare(evict, want) < 0) {
				want, bestPriorities, found = evict, values, true
			}
		}
		s := newCapacityPlanner(30, usage, priorities)
		require.Equal(t, want, s.EvictionFor("x", running), "trial %d", trial)
	}
}

func TestCapacity_UnloadAndRestart(t *testing.T) {
	a, b, c := newFakeProcess("a"), newFakeProcess("b"), newFakeProcess("c")
	a.autoReady, b.autoReady, c.autoReady = true, true, true
	r := newTestBase(t, map[string]process.Process{"a": a, "b": b, "c": c}, newCapacityPlanner(10, map[string]int64{"a": 4, "b": 6, "c": 6}, map[string]int64{"b": 10, "c": 10}))
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
				s.EvictionFor("x", running)
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
