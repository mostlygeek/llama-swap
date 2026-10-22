package router

import (
	"fmt"
	"maps"
	"slices"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// Capacity shares process and scheduling machinery with Group and Matrix.
type Capacity struct{ *baseRouter }

// NewCapacity validates resource limits and starts a router using the shared
// process lifecycle and scheduler with capacity-based eviction planning.
func NewCapacity(conf config.Config, logs *logmon.Group) (*Capacity, error) {
	capacity := conf.Routing.Router.Settings.Capacity
	if err := config.ValidateCapacity(capacity, conf.Models); err != nil {
		return nil, err
	}
	planner := &capacitySwapper{capacity: capacity, models: conf.Models, logger: logs.ProxyLogs}
	processes := make(map[string]process.Process, len(conf.Models))
	base, err := newBaseRouter("capacity", conf, processes, logs.ProxyLogs, planner)
	if err != nil {
		return nil, fmt.Errorf("creating base router: %w", err)
	}
	for id, model := range conf.Models {
		p, err := process.New(base.procCtx, id, model, logmon.NewWriter(logs.UpstreamLogs), logs.ProxyLogs)
		if err != nil {
			base.shutdownFn()
			base.procCancel()
			return nil, fmt.Errorf("creating process for %q: %w", id, err)
		}
		processes[id] = p
	}
	go base.run()
	return &Capacity{baseRouter: base}, nil
}

type capacitySwapper struct {
	capacity      *config.CapacityConfig
	searchLimit   int // zero uses the production node budget; tests may lower it
	searchNodes   int
	searchLimited bool
	models        map[string]config.ModelConfig
	logger        *logmon.Monitor

	// The scheduler calls planners from a single event-loop goroutine, with
	// the same inputs for EvictionFor and OnSwapStart. Reuse that decision so
	// logging does not repeat the search. Model configuration is immutable.
	lastTarget  string
	lastRunning []string
	lastEvict   []string
	lastValid   bool
}

// EvictionFor plans evictions for a resident snapshot, caching the decision for
// the scheduler's subsequent OnSwapStart call on the same event-loop goroutine.
func (s *capacitySwapper) EvictionFor(target string, running []string) []string {
	if s.lastValid && s.lastTarget == target && slices.Equal(s.lastRunning, running) {
		return s.lastEvict
	}
	evict := s.evictionFor(target, running)
	s.lastTarget = target
	s.lastRunning = slices.Clone(running)
	s.lastEvict = evict
	s.lastValid = true
	return evict
}

// evictionFor searches resident models, including pending starts, minimizing
// total cost, then eviction count, then sorted IDs. Costs are counted once even
// when it releases several resources. A feasible greedy seed keeps the decision
// safe if the node budget is exhausted; optimality is not guaranteed then.
func (s *capacitySwapper) evictionFor(target string, running []string) []string {
	s.searchNodes, s.searchLimited = 0, false
	pools := slices.Sorted(maps.Keys(s.capacity.Pools))
	totals := make([]int64, len(pools))
	need := make([]int64, len(pools))
	for p, pool := range pools {
		totals[p] = s.capacity.Pools[pool]
		need[p] = s.models[target].Capacities[pool]
	}
	ids := slices.Clone(running)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var candidates []string
	var usage [][]int64
	for _, id := range ids {
		if id == target {
			continue
		}
		candidates = append(candidates, id)
		vector := make([]int64, len(pools))
		for p, pool := range pools {
			vector[p] = s.models[id].Capacities[pool]
		}
		usage = append(usage, vector)
		for p, amount := range vector {
			need[p] += amount
		}
	}
	fits := func(freed []int64) bool {
		for p := range need {
			if freed[p] < need[p] {
				return false
			}
		}
		return true
	}
	for p := range need {
		need[p] -= totals[p]
	}
	if fits(make([]int64, len(totals))) {
		return nil
	}
	// Suffix sums also bound feasibility without overflowing validated totals.
	suffix := make([][]int64, len(candidates)+1)
	suffix[len(candidates)] = make([]int64, len(totals))
	for i := len(candidates) - 1; i >= 0; i-- {
		suffix[i] = slices.Clone(suffix[i+1])
		for p, amount := range usage[i] {
			suffix[i][p] += amount
		}
	}
	// Start by evicting all candidates, then retain expensive models whenever
	// possible. Deterministic ordering makes budget-limited results repeatable.
	order := make([]int, len(candidates))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		ca, cb := s.models[candidates[a]].EvictCost, s.models[candidates[b]].EvictCost
		if ca > cb {
			return -1
		}
		if ca < cb {
			return 1
		}
		return a - b
	})
	freed := slices.Clone(suffix[0])
	selected := make([]bool, len(candidates))
	for i := range selected {
		selected[i] = true
	}
	for _, i := range order {
		for p, amount := range usage[i] {
			freed[p] -= amount
		}
		if fits(freed) {
			selected[i] = false
		} else {
			for p, amount := range usage[i] {
				freed[p] += amount
			}
		}
	}
	var best []string
	var bestCost int64
	for i, id := range candidates {
		if selected[i] {
			best = append(best, id)
			bestCost += s.models[id].EvictCost
		}
	}
	limit := s.searchLimit
	if limit <= 0 {
		limit = 100000
	}
	clear(freed)
	chosen := make([]string, 0, len(candidates))
	var search func(int, int64)
	search = func(i int, cost int64) {
		if s.searchNodes >= limit {
			s.searchLimited = true
			return
		}
		s.searchNodes++
		if fits(freed) {
			if cost < bestCost || cost == bestCost && (len(chosen) < len(best) || len(chosen) == len(best) && slices.Compare(chosen, best) < 0) {
				best = slices.Clone(chosen)
				bestCost = cost
			}
			return
		}
		// Equal costs can still improve count or IDs, especially when costs
		// default to zero. An infeasible partial set needs at least one more
		// victim, so it cannot improve a same-cost set of this size or smaller.
		if i == len(candidates) || cost > bestCost || cost == bestCost && len(chosen) >= len(best) {
			return
		}
		for p := range need {
			if freed[p]+suffix[i][p] < need[p] {
				return
			}
		}
		chosen = append(chosen, candidates[i])
		for p, amount := range usage[i] {
			freed[p] += amount
		}
		search(i+1, cost+s.models[candidates[i]].EvictCost)
		for p, amount := range usage[i] {
			freed[p] -= amount
		}
		chosen = chosen[:len(chosen)-1]
		search(i+1, cost)
	}
	search(0, 0)
	if s.searchLimited {
		// A cheaper result found before the cutoff can still contain redundant
		// zero-cost victims. Retain each removable model even if the search
		// did not have time to improve the eviction-count tie breaker.
		clear(freed)
		for _, id := range best {
			for p, pool := range pools {
				freed[p] += s.models[id].Capacities[pool]
			}
		}
		for i := len(best) - 1; i >= 0; i-- {
			model := s.models[best[i]]
			for p, pool := range pools {
				freed[p] -= model.Capacities[pool]
			}
			if fits(freed) {
				best = slices.Delete(best, i, i+1)
			} else {
				for p, pool := range pools {
					freed[p] += model.Capacities[pool]
				}
			}
		}
	}
	return best
}

// OnSwapStart logs the cached eviction plan when the scheduler begins a swap.
func (s *capacitySwapper) OnSwapStart(target string, running []string) {
	evict := s.EvictionFor(target, running)
	if len(evict) > 0 {
		s.logger.Infof("capacity: model=%s evict=%v pools=%v search_nodes=%d search_limited=%t", target, evict, s.capacity.Pools, s.searchNodes, s.searchLimited)
	}
}
