package router

import (
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// redlineInterval is how often the runtime watchdog samples memory. It is
// deliberately much faster than the perf monitor's >=5s poll: enforcement is
// racing allocation, and each sample is a couple of sysfs/procfs reads
// (microseconds).
const redlineInterval = time.Second

// redlineLoop is the runtime enforcement tier of the memory gate. Admission
// (EnsureFits) can only reason about footprints it was told about; this loop
// catches everything admission cannot see: stale vramEstimateMB values,
// KV/compute growth after load, and out-of-band allocations. When live usage
// crosses the red line (or host MemAvailable drops under the floor) it evicts
// least-recently-used models until usage is back under the BUDGET (not the red
// line: the gap is hysteresis so it does not immediately re-trigger).
//
// Two tiers:
//   - red line: evict LRU models but spare the most-recently-used one (the
//     best proxy for "actively serving").
//   - hard line (or host floor halved): spare nothing. Losing an active model
//     beats the kernel TTM eviction stall -> hardware-watchdog reboot that
//     follows unchecked growth on a UMA box.
//
// The loop only acts on FRESH reads (direct sysfs); if the platform cannot
// provide them the watchdog stays dormant rather than acting on stale data.
func (b *baseRouter) redlineLoop() {
	g := b.memGate
	if g == nil || g.redlineMB <= 0 {
		return
	}
	if g.liveRead == nil {
		b.logger.Warn("redline: no fresh GPU memory reader available on this platform; runtime enforcement disabled")
		return
	}

	ticker := time.NewTicker(redlineInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.shutdownCtx.Done():
			return
		case <-ticker.C:
			g.enforceRedline(b.processes, b.logger)
		}
	}
}

// hostBreach classifies the host-RAM condition: 0 = fine (or unreadable),
// 1 = below floor (red), 2 = below half the floor (hard).
func (g *memGate) hostBreach() int {
	if g.hostFloorMB <= 0 || g.hostAvail == nil {
		return 0
	}
	avail, ok := g.hostAvail()
	if !ok {
		return 0
	}
	switch {
	case avail < g.hostFloorMB/2:
		return 2
	case avail < g.hostFloorMB:
		return 1
	default:
		return 0
	}
}

// enforceRedline performs one watchdog pass: sample, classify, and evict until
// usage is back under budget and the host floor. Safe to call concurrently
// with admissions; it takes the same mutex, so a pass and an admission never
// interleave their evictions.
func (g *memGate) enforceRedline(procs map[string]process.Process, log *logmon.Monitor) {
	if g == nil || g.redlineMB <= 0 || g.liveRead == nil {
		return
	}

	used, ok := g.liveRead()
	if !ok {
		return
	}
	host := g.hostBreach()
	if used <= g.redlineMB && host == 0 {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	hard := host >= 2 || (g.hardlineMB > 0 && used > g.hardlineMB)
	if hard {
		log.Warnf("redline: HARD breach (used=%dMB, hardline=%dMB, hostBreach=%d); evicting including active models",
			used, g.hardlineMB, host)
	} else {
		log.Warnf("redline: breach (used=%dMB > redline=%dMB or host floor); evicting idle LRU models",
			used, g.redlineMB)
	}

	// Reservation holders are mid-load under an admission; killing them races
	// that swap. The admission that reserved them already fit under budget, so
	// they are not the runaway.
	excluded := make(map[string]struct{}, len(g.reservations))
	for id := range g.reservations {
		excluded[id] = struct{}{}
	}

	for used > g.budgetMB || g.hostBreach() > 0 {
		victim, ok := g.pickLRUVictim(procs, excluded, !hard, hard)
		if !ok {
			log.Warnf("redline: still over (used=%dMB, budget=%dMB) but no evictable models remain", used, g.budgetMB)
			return
		}

		log.Warnf("redline: evicting %s (used=%dMB, budget=%dMB)", victim, used, g.budgetMB)
		if err := procs[victim].Stop(g.stopTimeout); err != nil {
			log.Warnf("redline: stopping %s failed: %v", victim, err)
		}
		excluded[victim] = struct{}{}

		settled, observed := g.settleAfterEviction(used, g.estimateMB(victim))
		used = settled
		if !observed && g.estimateMB(victim) <= 0 {
			// Unknown-size victim with no observable drop: bound to one
			// eviction per pass rather than draining the resident set blind.
			return
		}
	}

	log.Infof("redline: recovered (used=%dMB <= budget=%dMB)", used, g.budgetMB)
}
