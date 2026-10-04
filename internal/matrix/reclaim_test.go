package matrix

import (
	"math"
	"reflect"
	"testing"
)

// newReclaimProgram builds a budget-3 matrix over models a..e plus target t:
// requesting t with three of a..e running admits sets that evict exactly one,
// so every candidate scores the same default raw cost of 1.
func newReclaimProgram(t *testing.T) *Program {
	t.Helper()
	p, err := Compile([]Definition{
		{Name: "pool", DSL: "(t | a | b | c | d | e)"},
		{Name: "all", DSL: "+pool & +pool & +pool"},
	}, func(ident string) (string, bool) { return ident, true })
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return p
}

// ReclaimMinimal (the default, including the empty string) ignores Upcoming:
// exactly one model is evicted, as under the historical objective.
func TestProgram_SolveReclaimMinimalIgnoresUpcoming(t *testing.T) {
	p := newReclaimProgram(t)
	for _, reclaim := range []string{"", ReclaimMinimal} {
		result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
			Reclaim:  reclaim,
			Upcoming: []string{"d", "e"},
		})
		if len(result.Evict) != 1 {
			t.Fatalf("reclaim=%q: Evict=%v want exactly one eviction", reclaim, result.Evict)
		}
	}
}

// A non-empty queue does not widen the evict list: the count stays minimal
// (one for a full budget-3 fleet) so the fleet remains full and each queued
// connection meets exactly one idle model. It is the *choice* of idle model
// the queue steers, not the count.
func TestProgram_SolveReclaimQueueKeepsMinimalCount(t *testing.T) {
	p := newReclaimProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		Reclaim:  ReclaimQueue,
		Upcoming: []string{"d", "e"}, // queued but not running
	})
	if len(result.Evict) != 1 {
		t.Fatalf("Evict=%v want exactly one eviction (the queue must not widen the evict list)", result.Evict)
	}
	found := false
	for _, m := range result.TargetSet {
		if m == "t" {
			found = true
		}
	}
	if !found {
		t.Fatalf("TargetSet=%v must contain the target", result.TargetSet)
	}
}

// Eviction cost protects queued models: a running model still in the queue is
// kept and an idle unqueued model is evicted instead. The count is still one.
func TestProgram_SolveReclaimQueueProtectsQueued(t *testing.T) {
	p := newReclaimProgram(t)
	// b is queued and expensive; a and c are not in the queue.
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		Reclaim:    ReclaimQueue,
		EvictCosts: map[string]int{"b": 10},
		Upcoming:   []string{"b", "d"},
	})
	if len(result.Evict) != 1 {
		t.Fatalf("Evict=%v want one eviction (turn over one idle model)", result.Evict)
	}
	if result.Evict[0] == "b" {
		t.Fatalf("Evict=%v must not evict queued model b", result.Evict)
	}
}

// The charged cost is primary: an expensive queued model is kept and a single
// cheaper unqueued model is evicted instead.
func TestProgram_SolveReclaimQueueChargedPrimary(t *testing.T) {
	p := newReclaimProgram(t)
	// a is queued and costs 10. Keeping a evicts one of b or c (charged 0);
	// dropping a evicts a itself (charged 10). Charged wins: a is kept.
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		Reclaim:    ReclaimQueue,
		EvictCosts: map[string]int{"a": 10},
		Upcoming:   []string{"a"},
	})
	if len(result.Evict) != 1 {
		t.Fatalf("Evict=%v want one eviction", result.Evict)
	}
	if result.Evict[0] == "a" {
		t.Fatalf("Evict=%v must keep queued a despite its higher raw cost", result.Evict)
	}
}

// Queue reclaim with an empty queue behaves exactly as minimal: the evict
// list is the set complement only.
func TestProgram_SolveReclaimQueueEmptyQueueIsMinimal(t *testing.T) {
	p := newReclaimProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		Reclaim:  ReclaimQueue,
		Upcoming: nil,
	})
	if len(result.Evict) != 1 {
		t.Fatalf("Evict=%v want exactly one eviction (empty queue falls back to minimal)", result.Evict)
	}
}

// The load-test symptom: measured costs are all distinct, so there is no tie
// for a tie-breaker to order — the objective alone must do the steering. b is
// cheap but queued (about to run); a and c are idle and unqueued. The minimal
// objective evicts b (the cheapest to reload) and churns work the backlog is
// about to do. Queue reclaim charges only the queued models: evicting b
// scores its full cost, while evicting an unqueued model scores zero, so the
// cheapest *unequeued* model turns over and b stays warm for its request.
func TestProgram_SolveReclaimQueueSteersDistinctCosts(t *testing.T) {
	p := newReclaimProgram(t)
	costs := map[string]int{"a": 300000, "b": 30000, "c": 60000}

	minimal := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		Reclaim:    ReclaimMinimal,
		EvictCosts: costs,
		Upcoming:   []string{"b", "d"},
	})
	if !reflect.DeepEqual(minimal.Evict, []string{"b"}) {
		t.Fatalf("minimal Evict=%v want [b] (the cheapest to reload)", minimal.Evict)
	}

	queued := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		Reclaim:    ReclaimQueue,
		EvictCosts: costs,
		Upcoming:   []string{"b", "d"},
	})
	if !reflect.DeepEqual(queued.Evict, []string{"c"}) {
		t.Fatalf("queue Evict=%v want [c] (cheapest unqueued; queued b is protected)", queued.Evict)
	}
}

// Measured costs are saturated at int max per model, and a candidate can drop
// several of them: the rank sums must not wrap negative and invert the
// ordering. The candidate dropping two saturated models must rank above the
// one dropping a single saturated model.
func TestProgram_SolveRankSumsDoNotWrap(t *testing.T) {
	p := newReclaimProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		EvictCosts: map[string]int{"a": math.MaxInt, "b": math.MaxInt, "c": 1},
	})
	if !reflect.DeepEqual(result.Evict, []string{"c"}) {
		t.Fatalf("Evict=%v want [c] (a single cheap eviction beats two saturated ones)", result.Evict)
	}
	if result.TotalCost != 1 {
		t.Fatalf("TotalCost=%d want 1", result.TotalCost)
	}
}

// A reserved model is loading (an in-flight swap's target), not idle: even
// though it is the cheapest to reload, it must not be evicted while an idle
// unreserved alternative exists.
func TestProgram_SolveReservedNeverEvictedWhenIdleExists(t *testing.T) {
	p := newReclaimProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		// b is reserved and the cheapest; a and c are idle.
		EvictCosts: map[string]int{"a": 100, "b": 1, "c": 100},
		Reserved:   []string{"b"},
	})
	if len(result.Evict) != 1 {
		t.Fatalf("Evict=%v want exactly one eviction", result.Evict)
	}
	if result.Evict[0] == "b" {
		t.Fatalf("Evict=%v must not evict reserved (loading) b while idle a/c exist", result.Evict)
	}
}

// When every candidate must evict a reserved model there is no idle slot:
// the solver falls back to the plain ranking (cheapest reserved model), and
// the scheduler's collision check parks the request until a slot frees up.
func TestProgram_SolveReservedFallbackWhenNoIdleExists(t *testing.T) {
	p := newReclaimProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		EvictCosts: map[string]int{"a": 1, "b": 2, "c": 3},
		Reserved:   []string{"a", "b", "c"},
	})
	if !reflect.DeepEqual(result.Evict, []string{"a"}) {
		t.Fatalf("Evict=%v want [a] (plain ranking fallback; the scheduler queues the request)", result.Evict)
	}
}

// Reserved and queue reclaim compose: the queued model is protected by the
// charged cost and the reserved (loading) model is protected by the
// reservation, so the single eviction lands on the remaining idle model.
func TestProgram_SolveReservedComposesWithQueueReclaim(t *testing.T) {
	p := newReclaimProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		Reclaim:    ReclaimQueue,
		EvictCosts: map[string]int{"a": 1, "b": 10, "c": 1},
		Upcoming:   []string{"b"}, // b is queued and running
		Reserved:   []string{"c"}, // c is loading
	})
	if !reflect.DeepEqual(result.Evict, []string{"a"}) {
		t.Fatalf("Evict=%v want [a] (queued b protected, reserved c protected)", result.Evict)
	}
}
