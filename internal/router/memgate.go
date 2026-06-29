package router

import (
	"sort"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// memProbe reports current GPU-managed memory usage in MiB. It is an interface
// so the gate can be unit-tested with a fake; the production implementation is
// perfMemProbe, backed by perf.Monitor.
type memProbe interface {
	// liveUsedMB returns the most recent total GPU memory in use across all
	// GPUs (VRAM + GTT, per the sysfs backend), or (0, false) when no reading
	// is available yet.
	liveUsedMB() (int, bool)
}

// perfMemProbe adapts a perf.Monitor to memProbe. After the sysfs/GTT patch,
// GpuStat.MemUsedMB already includes GTT (where APU/iGPU models and their KV
// cache actually live), which is exactly what we want to budget against.
type perfMemProbe struct {
	mon *perf.Monitor
}

func (p perfMemProbe) liveUsedMB() (int, bool) {
	_, gpuStats := p.mon.Current()
	if len(gpuStats) == 0 {
		return 0, false
	}

	// Current() returns a flat slice spanning several poll timestamps for each
	// GPU. Collapse to the latest sample per GPU id, then sum across GPUs so a
	// multi-GPU box budgets against its whole pool.
	latest := make(map[int]perf.GpuStat)
	for _, g := range gpuStats {
		if prev, ok := latest[g.ID]; !ok || g.Timestamp.After(prev.Timestamp) {
			latest[g.ID] = g
		}
	}

	total := 0
	for _, g := range latest {
		total += g.MemUsedMB
	}
	return total, true
}

// memGate is an optional, fail-open admission gate that keeps total GPU memory
// under a configured budget by evicting least-recently-used models before a
// swap. It is a standalone control at the single launch chokepoint
// (baseRouter.doSwap) and deliberately does NOT participate in the matrix
// solver's eviction decisions: it only adds further evictions when live usage
// (which includes hard-to-predict GTT + KV-cache growth) would push the next
// load over budget.
type memGate struct {
	budgetMB int
	probe    memProbe
	// estimates maps model id -> configured VramEstimateMB, used to project a
	// load's footprint before it happens. Missing/zero means "unknown" and the
	// gate behaves reactively for that model.
	estimates map[string]int
	// stopTimeout bounds each synchronous victim Stop. Stopping a process frees
	// its GPU memory, so EnsureFits waits for it before re-checking the budget.
	stopTimeout time.Duration
}

// newMemGate returns a gate, or nil when it should be disabled (no budget or no
// probe). A nil *memGate is safe: callers guard with `if b.memGate != nil`.
func newMemGate(budgetMB int, mon *perf.Monitor, estimates map[string]int, stopTimeout time.Duration) *memGate {
	if budgetMB <= 0 || mon == nil {
		return nil
	}
	return &memGate{
		budgetMB:    budgetMB,
		probe:       perfMemProbe{mon: mon},
		estimates:   estimates,
		stopTimeout: stopTimeout,
	}
}

// newMemGateFromConfig builds the gate for a router from the parsed config and
// the (possibly nil) perf monitor. It returns nil when the budget is unset or
// no monitor is available, leaving the gate disabled.
func newMemGateFromConfig(conf config.Config, mon *perf.Monitor) *memGate {
	budget := conf.Performance.GpuBudgetMB
	if budget <= 0 || mon == nil {
		return nil
	}

	estimates := make(map[string]int, len(conf.Models))
	for id, mc := range conf.Models {
		if mc.VramEstimateMB > 0 {
			estimates[id] = mc.VramEstimateMB
		}
	}

	stopTimeout := time.Duration(conf.HealthCheckTimeout) * time.Second
	if stopTimeout <= 0 {
		stopTimeout = 30 * time.Second
	}

	return newMemGate(budget, mon, estimates, stopTimeout)
}

// EnsureFits evicts least-recently-used resident models, synchronously, until
// the projected footprint (current live usage + the incoming model's estimate)
// fits under the budget, or there is nothing left to evict.
//
// It is FAIL-OPEN by design: any condition it cannot resolve (no probe reading
// yet, no eligible victim, budget still exceeded after evicting everything it
// may) results in a warning and a return that lets the load proceed. Starving a
// load is worse than briefly overshooting a soft budget.
//
// incoming is the model about to be loaded (never evicted). alreadyEvicting are
// models the caller is already stopping for this swap (the solver's decision);
// they are excluded from victim selection and assumed already freed.
func (g *memGate) EnsureFits(
	incoming string,
	procs map[string]process.Process,
	alreadyEvicting []string,
	log *logmon.Monitor,
) {
	if g == nil || g.budgetMB <= 0 {
		return
	}

	used, ok := g.probe.liveUsedMB()
	if !ok {
		// No reading available; fail open rather than blindly evicting.
		log.Debugf("memgate: no GPU reading available, skipping budget check for %s", incoming)
		return
	}

	estimate := g.estimateMB(incoming)
	projected := used + estimate
	if projected <= g.budgetMB {
		log.Debugf("memgate: %s fits (used=%dMB + est=%dMB = %dMB <= budget=%dMB)",
			incoming, used, estimate, projected, g.budgetMB)
		return
	}

	log.Infof("memgate: %s over budget (used=%dMB + est=%dMB = %dMB > budget=%dMB), evicting LRU models",
		incoming, used, estimate, projected, g.budgetMB)

	excluded := make(map[string]struct{}, len(alreadyEvicting)+1)
	excluded[incoming] = struct{}{}
	for _, id := range alreadyEvicting {
		excluded[id] = struct{}{}
	}

	for projected > g.budgetMB {
		victim, ok := g.pickLRUVictim(procs, excluded)
		if !ok {
			// Nothing left to evict: fail open.
			log.Warnf("memgate: %s still over budget (projected=%dMB > budget=%dMB) but no evictable models remain; loading anyway",
				incoming, projected, g.budgetMB)
			return
		}

		log.Infof("memgate: evicting LRU model %s to free GPU memory for %s", victim, incoming)
		if err := procs[victim].Stop(g.stopTimeout); err != nil {
			log.Warnf("memgate: stopping %s failed: %v", victim, err)
		}
		excluded[victim] = struct{}{}

		// Re-read live usage: the Stop above has freed the victim's GPU memory,
		// so the next decision is based on the real post-eviction footprint
		// rather than arithmetic guesswork.
		if reUsed, ok := g.probe.liveUsedMB(); ok {
			used = reUsed
		}
		projected = used + estimate
	}

	log.Infof("memgate: %s now fits (projected=%dMB <= budget=%dMB)", incoming, projected, g.budgetMB)
}

// pickLRUVictim returns the resident (non-stopped) model with the oldest
// LastUse() that is not excluded. ok is false when no candidate exists.
func (g *memGate) pickLRUVictim(
	procs map[string]process.Process,
	excluded map[string]struct{},
) (string, bool) {
	type candidate struct {
		id      string
		lastUse int64
	}
	var candidates []candidate
	for id, p := range procs {
		if _, skip := excluded[id]; skip {
			continue
		}
		switch p.State() {
		case process.StateStopped, process.StateShutdown:
			continue // not resident, frees no GPU memory
		}
		candidates = append(candidates, candidate{id: id, lastUse: p.LastUse()})
	}
	if len(candidates) == 0 {
		return "", false
	}
	// Oldest LastUse first; tie-break on id for deterministic behaviour.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].lastUse != candidates[j].lastUse {
			return candidates[i].lastUse < candidates[j].lastUse
		}
		return candidates[i].id < candidates[j].id
	})
	return candidates[0].id, true
}

// estimateMB returns the incoming model's configured VRAM estimate in MiB, or 0
// when unknown. A 0 estimate makes the gate reactive: it evicts only once live
// usage already exceeds the budget, never pre-emptively.
func (g *memGate) estimateMB(incoming string) int {
	return g.estimates[incoming]
}
