package matrix

import (
	"reflect"
	"testing"
	"time"
)

// newTieBreakerProgram builds a budget-3 matrix over models t, a, b, and c:
// requesting t with a, b, and c running must evict exactly one of the three.
// Eviction costs are supplied per Solve call via SolveOptions.
func newTieBreakerProgram(t *testing.T) *Program {
	t.Helper()
	p, err := Compile([]Definition{
		{Name: "pool", DSL: "(t | a | b | c)"},
		{Name: "all",  DSL: "+pool & +pool & +pool"},
	}, func(ident string) (string, bool) { return ident, true })
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return p
}

// In lexical mode (the default, including the empty string) the first
// equal-cost candidate in definition order wins; idle data is ignored.
func TestProgram_SolveTieBreakerLexical(t *testing.T) {
	p := newTieBreakerProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		Idle: map[string]time.Duration{"a": time.Hour},
	})
	if !reflect.DeepEqual(result.Evict, []string{"c"}) {
		t.Fatalf("Evict=%v want [c] (lexical first-candidate wins, idle ignored)", result.Evict)
	}
}

// In lru mode the candidate evicting the longest-idle model wins.
func TestProgram_SolveTieBreakerLRUEvictsIdlest(t *testing.T) {
	p := newTieBreakerProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		TieBreaker: TieBreakerLRU,
		Idle: map[string]time.Duration{
			"a": 10 * time.Minute,
			"b": time.Hour,
			"c": time.Minute,
		},
	})
	if !reflect.DeepEqual(result.Evict, []string{"b"}) {
		t.Fatalf("Evict=%v want [b] (longest idle)", result.Evict)
	}
}

// Eviction cost stays the primary key: an expensive model is never evicted
// on a cost tie, no matter how idle it is.
func TestProgram_SolveTieBreakerLRUCostDominatesIdle(t *testing.T) {
	p := newTieBreakerProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		TieBreaker: TieBreakerLRU,
		EvictCosts: map[string]int{"c": 10},
		Idle: map[string]time.Duration{
			"a": 10 * time.Minute,
			"b": time.Minute,
			"c": 2 * time.Hour, // idlest, but costs 10 to evict
		},
	})
	if !reflect.DeepEqual(result.Evict, []string{"a"}) {
		t.Fatalf("Evict=%v want [a] (cheapest cost wins; among cost-1 candidates the idlest, a, is evicted)", result.Evict)
	}
}

// When idle times are equal, the residual tie falls back to definition
// order, keeping outcomes deterministic.
func TestProgram_SolveTieBreakerLRUResidualTie(t *testing.T) {
	p := newTieBreakerProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		TieBreaker: TieBreakerLRU,
		Idle: map[string]time.Duration{
			"a": 5 * time.Minute,
			"b": 5 * time.Minute,
			"c": time.Minute,
		},
	})
	if !reflect.DeepEqual(result.Evict, []string{"b"}) {
		t.Fatalf("Evict=%v want [b] (first of the equal-idle pair in definition order)", result.Evict)
	}
}

// A running model missing from the idle map is treated as idle for zero.
func TestProgram_SolveTieBreakerLRUUnknownIdleIsZero(t *testing.T) {
	p := newTieBreakerProgram(t)
	result := p.Solve("t", []string{"a", "b", "c"}, SolveOptions{
		TieBreaker: TieBreakerLRU,
		Idle:       map[string]time.Duration{"a": time.Hour},
	})
	if !reflect.DeepEqual(result.Evict, []string{"a"}) {
		t.Fatalf("Evict=%v want [a] (b and c have no idle data and rank zero)", result.Evict)
	}
}
