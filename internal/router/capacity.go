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
// by the scheduler. Minimize eviction count first. For equal counts, compare
// ascending priority vectors, preferring the larger first difference: this
// protects the lowest-numbered models whenever possible. Remaining ties use
// lexicographically sorted model IDs. No priority scores are added together.
// Suffix usage sums and the best count prune the search. Worst-case time is
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
	priorities := func(ids []string, values []int64) []int64 {
		values = values[:len(ids)]
		for i, id := range ids {
			values[i] = s.models[id].Capacity.EvictionPriority
		}
		slices.Sort(values)
		return values
	}
	// Evicting every candidate is always feasible after validation.
	best := slices.Clone(candidates)
	bestPriorities := priorities(best, make([]int64, len(candidates)))
	priorityBuffer := make([]int64, len(candidates))
	chosen := make([]string, 0, len(candidates))
	var search func(int, int64)
	search = func(i int, freed int64) {
		if freed >= need {
			values := priorities(chosen, priorityBuffer)
			comparison := slices.Compare(values, bestPriorities)
			if len(chosen) < len(best) || len(chosen) == len(best) && (comparison > 0 || comparison == 0 && slices.Compare(chosen, best) < 0) {
				best = slices.Clone(chosen)
				bestPriorities = append(bestPriorities[:0], values...)
			}
			return
		}
		if i == len(candidates) || freed+suffix[i] < need || len(chosen) >= len(best) {
			return
		}
		id := candidates[i]
		chosen = append(chosen, id)
		search(i+1, freed+s.models[id].Capacity.Usage)
		chosen = chosen[:len(chosen)-1]
		search(i+1, freed)
	}
	search(0, 0)
	return best
}

func (s *capacitySwapper) OnSwapStart(target string, running []string) {
	evict := s.EvictionFor(target, running)
	if len(evict) > 0 {
		s.logger.Infof("capacity: model=%s evict=%v total=%d", target, evict, s.total)
	}
}
