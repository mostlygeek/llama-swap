package router

import (
	"fmt"
	"slices"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// Capacity shares process and scheduling machinery with Group and Matrix.
type Capacity struct{ *baseRouter }

func NewCapacity(conf config.Config, logs *logmon.Group) (*Capacity, error) {
	capacity := conf.Routing.Router.Settings.Capacity
	if err := config.ValidateCapacity(capacity, conf.Models); err != nil {
		return nil, err
	}
	planner := &capacitySwapper{total: capacity.Total, models: conf.Models, logger: logs.ProxyLogs}
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
	total  int64
	models map[string]config.ModelConfig
	logger *logmon.Monitor
}

// EvictionFor searches only resident models, including pending starts supplied
// by the scheduler. Minimize total eviction cost, then eviction count, then
// lexicographically sorted model IDs. Higher costs protect expensive models.
// Suffix usage sums and the best cost prune the search. Worst-case time is
// exponential in resident count; no configured combinations are generated.
func (s *capacitySwapper) EvictionFor(target string, running []string) []string {
	ids := slices.Clone(running)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var candidates []string
	var used int64
	for _, id := range ids {
		if id != target {
			candidates = append(candidates, id)
			used += s.models[id].Capacity.Usage
		}
	}
	need := used - (s.total - s.models[target].Capacity.Usage)
	if need <= 0 {
		return nil
	}
	suffix := make([]int64, len(candidates)+1)
	for i := len(candidates) - 1; i >= 0; i-- {
		suffix[i] = suffix[i+1] + s.models[candidates[i]].Capacity.Usage
	}
	// Evicting every candidate is always feasible after validation.
	best := slices.Clone(candidates)
	var bestCost int64
	for _, id := range best {
		bestCost += s.models[id].Capacity.EvictCost
	}
	chosen := make([]string, 0, len(candidates))
	var search func(int, int64, int64)
	search = func(i int, freed, cost int64) {
		if freed >= need {
			if cost < bestCost || cost == bestCost && (len(chosen) < len(best) || len(chosen) == len(best) && slices.Compare(chosen, best) < 0) {
				best = slices.Clone(chosen)
				bestCost = cost
			}
			return
		}
		if i == len(candidates) || freed+suffix[i] < need || cost >= bestCost {
			return
		}
		id := candidates[i]
		chosen = append(chosen, id)
		search(i+1, freed+s.models[id].Capacity.Usage, cost+s.models[id].Capacity.EvictCost)
		chosen = chosen[:len(chosen)-1]
		search(i+1, freed, cost)
	}
	search(0, 0, 0)
	return best
}

func (s *capacitySwapper) OnSwapStart(target string, running []string) {
	evict := s.EvictionFor(target, running)
	if len(evict) > 0 {
		s.logger.Infof("capacity: model=%s evict=%v total=%d", target, evict, s.total)
	}
}
