package router

import (
	"fmt"
	"slices"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// Resource shares process and scheduling machinery with Group and Matrix.
type Resource struct{ *baseRouter }

func NewResource(conf config.Config, logs *logmon.Group) (*Resource, error) {
	resource := conf.Routing.Router.Settings.Resource
	if err := config.ValidateResource(resource, conf.Models); err != nil {
		return nil, err
	}
	planner := &resourceSwapper{config: *resource, logger: logs.ProxyLogs}
	processes := make(map[string]process.Process, len(conf.Models))
	base, err := newBaseRouter("resource", conf, processes, logs.ProxyLogs, planner)
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
	return &Resource{baseRouter: base}, nil
}

type resourceSwapper struct {
	config config.ResourceConfig
	logger *logmon.Monitor
}

// EvictionFor searches only resident models, including pending starts supplied
// by the scheduler. Positive costs let us stop exploring a feasible branch:
// adding another eviction can only make it worse. Suffix resource sums prune
// branches that cannot free enough. Worst-case time is exponential in resident
// count, with linear space; no configured model combinations are generated.
// Equal costs prefer fewer evictions, then lexicographically smaller sorted IDs.
func (s *resourceSwapper) EvictionFor(target string, running []string) []string {
	ids := slices.Clone(running)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var candidates []string
	var used int64
	for _, id := range ids {
		if id != target {
			candidates = append(candidates, id)
			used += s.config.Requirements[id]
		}
	}
	// Subtract rather than add the request, keeping arithmetic in int64 range.
	need := used - (s.config.Capacity - s.config.Requirements[target])
	if need <= 0 {
		return nil
	}
	suffix := make([]int64, len(candidates)+1)
	for i := len(candidates) - 1; i >= 0; i-- {
		suffix[i] = suffix[i+1] + s.config.Requirements[candidates[i]]
	}
	costOf := func(id string) int64 {
		if cost, ok := s.config.EvictCosts[id]; ok {
			return int64(cost)
		}
		return 1
	}
	// Evicting every candidate is always feasible after validation.
	best := slices.Clone(candidates)
	var bestCost int64
	for _, id := range best {
		bestCost += costOf(id)
	}
	chosen := make([]string, 0, len(candidates))
	var search func(int, int64, int64)
	search = func(i int, freed, cost int64) {
		if freed >= need {
			if cost < bestCost || cost == bestCost && (len(chosen) < len(best) || len(chosen) == len(best) && slices.Compare(chosen, best) < 0) {
				best, bestCost = slices.Clone(chosen), cost
			}
			return
		}
		if i == len(candidates) || freed+suffix[i] < need || cost >= bestCost {
			return
		}
		id := candidates[i]
		chosen = append(chosen, id)
		search(i+1, freed+s.config.Requirements[id], cost+costOf(id))
		chosen = chosen[:len(chosen)-1]
		search(i+1, freed, cost)
	}
	search(0, 0, 0)
	return best
}

func (s *resourceSwapper) OnSwapStart(target string, running []string) {
	evict := s.EvictionFor(target, running)
	if len(evict) > 0 {
		s.logger.Infof("resource: model=%s evict=%v capacity=%d", target, evict, s.config.Capacity)
	}
}
