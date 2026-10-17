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

func TestResource_EvictionFor(t *testing.T) {
	for _, tc := range []struct {
		name          string
		capacity      int64
		requirements  map[string]int64
		costs         map[string]int
		running, want []string
	}{
		{"empty", 10, map[string]int64{"x": 10}, nil, nil, nil},
		{"below capacity", 10, map[string]int64{"a": 2, "x": 3}, nil, []string{"a"}, nil},
		{"exact capacity", 10, map[string]int64{"a": 4, "x": 6}, nil, []string{"a"}, nil},
		{"already resident", 10, map[string]int64{"a": 4, "x": 6}, nil, []string{"x", "a"}, nil},
		{"single", 10, map[string]int64{"a": 8, "x": 6}, nil, []string{"a"}, []string{"a"}},
		{"multiple", 10, map[string]int64{"a": 4, "b": 4, "x": 9}, nil, []string{"b", "a"}, []string{"a", "b"}},
		// Greedy cheapest-first evicts a then b for cost 3. Only b is needed, cost 2.
		{"complete solution", 10, map[string]int64{"a": 2, "b": 8, "x": 7}, map[string]int{"a": 1, "b": 2}, []string{"a", "b"}, []string{"b"}},
		{"retain costly", 10, map[string]int64{"a": 4, "b": 6, "x": 4}, map[string]int{"a": 10}, []string{"a", "b"}, []string{"b"}},
		{"cheaper pair", 10, map[string]int64{"a": 5, "b": 2, "c": 3, "x": 5}, map[string]int{"a": 10}, []string{"a", "b", "c"}, []string{"b", "c"}},
		{"tie count", 10, map[string]int64{"a": 2, "b": 3, "c": 5, "x": 5}, map[string]int{"c": 2}, []string{"a", "b", "c"}, []string{"c"}},
		{"tie IDs", 10, map[string]int64{"a": 5, "b": 5, "x": 5}, nil, []string{"b", "a"}, []string{"a"}},
		{"maximum capacity", 9223372036854775807, map[string]int64{"a": 9223372036854775806, "x": 1}, nil, []string{"a"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &resourceSwapper{config: config.ResourceConfig{Capacity: tc.capacity, Requirements: tc.requirements, EvictCosts: tc.costs}}
			original := slices.Clone(tc.running)
			require.Equal(t, tc.want, s.EvictionFor("x", tc.running))
			require.Equal(t, original, tc.running, "must not mutate scheduler snapshot")
			slices.Reverse(tc.running)
			require.Equal(t, tc.want, s.EvictionFor("x", tc.running))
		})
	}
}

// Compare the pruned search to a separate exhaustive oracle on small random
// budgets. This covers interacting cost, count and ID ties across whole sets.
func TestResource_ExhaustiveOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(1004))
	for trial := 0; trial < 500; trial++ {
		r := config.ResourceConfig{Capacity: 30, Requirements: map[string]int64{"x": int64(1 + rng.Intn(30))}, EvictCosts: map[string]int{}}
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
			r.Requirements[id] = amount
			r.EvictCosts[id] = 1 + rng.Intn(6)
		}
		var want []string
		bestCost := int64(1 << 62)
		for mask := 0; mask < 1<<len(running); mask++ {
			var evict []string
			var cost, freed int64
			for i, id := range running {
				if mask&(1<<i) != 0 {
					evict = append(evict, id)
					cost += int64(r.EvictCosts[id])
					freed += r.Requirements[id]
				}
			}
			if used-freed+r.Requirements["x"] > r.Capacity {
				continue
			}
			if cost < bestCost || cost == bestCost && (len(evict) < len(want) || len(evict) == len(want) && slices.Compare(evict, want) < 0) {
				want, bestCost = evict, cost
			}
		}
		s := &resourceSwapper{config: r}
		require.Equal(t, want, s.EvictionFor("x", running), "trial %d", trial)
	}
}

func TestResource_UnloadAndRestart(t *testing.T) {
	a, b, c := newFakeProcess("a"), newFakeProcess("b"), newFakeProcess("c")
	a.autoReady, b.autoReady, c.autoReady = true, true, true
	r := newTestBase(t, map[string]process.Process{"a": a, "b": b, "c": c}, &resourceSwapper{
		config: config.ResourceConfig{Capacity: 10, Requirements: map[string]int64{"a": 4, "b": 6, "c": 6}, EvictCosts: map[string]int{"a": 10}},
		logger: logmon.NewWriter(io.Discard),
	})
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

func BenchmarkResource_EvictionFor(b *testing.B) {
	for _, n := range []int{8, 16, 24} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			r := config.ResourceConfig{Capacity: int64(n), Requirements: map[string]int64{"x": int64(n / 2)}}
			var running []string
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("m%02d", i)
				running = append(running, id)
				r.Requirements[id] = 1
			}
			s := &resourceSwapper{config: r}
			b.ReportAllocs()
			for b.Loop() {
				s.EvictionFor("x", running)
			}
		})
	}
}

// Three requests arrive while the first two starts are pending. The third
// cannot consume unreserved capacity, or stop a chosen victim mid-request.
func TestResource_PendingStartsAndBusyEviction(t *testing.T) {
	a, b, c := newFakeProcess("a"), newFakeProcess("b"), newFakeProcess("c")
	a.serveBlock = make(chan struct{})
	c.autoReady = true
	r := newTestBase(t, map[string]process.Process{"a": a, "b": b, "c": c}, &resourceSwapper{
		config: config.ResourceConfig{Capacity: 10, Requirements: map[string]int64{"a": 4, "b": 4, "c": 4}},
		logger: logmon.NewWriter(io.Discard),
	})
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

func TestResource_NewRejectsInvalidBudget(t *testing.T) {
	_, err := NewResource(config.Config{}, logmon.NewGroup(io.Discard, true, true, true))
	require.ErrorContains(t, err, "resource configuration is required")
}
