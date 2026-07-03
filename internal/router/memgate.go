package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// admissionRetryInterval is how often a strict-mode admission re-checks the
// budget while waiting (up to AdmissionMaxWait) for memory to free up.
const admissionRetryInterval = 2 * time.Second

// evictionSettleTimeout bounds how long, after stopping a victim, the gate
// polls a fresh memory reading waiting to observe the freed memory before
// falling back to arithmetic accounting.
const evictionSettleTimeout = 2 * time.Second

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

// MemoryBudgetError is the typed rejection a strict-mode admission returns
// when a model load cannot fit under the configured memory budget. It
// implements shared.HTTPError so the renderer sends a 503 with Retry-After
// instead of a generic 500: the condition is transient (TTL unloads and
// finished swaps free memory), so clients should back off and retry.
type MemoryBudgetError struct {
	Model      string
	Detail     string
	RetryAfter int
}

func (e *MemoryBudgetError) Error() string {
	return fmt.Sprintf("memory budget exceeded loading %s: %s", e.Model, e.Detail)
}

func (e *MemoryBudgetError) StatusCode() int { return http.StatusServiceUnavailable }

func (e *MemoryBudgetError) Header() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	retry := e.RetryAfter
	if retry <= 0 {
		retry = 10
	}
	h.Set("Retry-After", strconv.Itoa(retry))
	return h
}

func (e *MemoryBudgetError) Body() []byte {
	b, _ := json.Marshal(map[string]string{"error": e.Error()})
	return b
}

// memGate is an admission gate at the single launch chokepoint
// (baseRouter.doSwap) that keeps total GPU memory under a configured budget by
// evicting least-recently-used models before a swap. It deliberately does NOT
// participate in the matrix solver's eviction decisions: it only adds further
// evictions when live usage (which includes hard-to-predict GTT + KV-cache
// growth) would push the next load over budget.
//
// Admissions are SERIALIZED (one mutex) and RESERVED: every admitted load
// holds a reservation of its configured estimate until the caller Releases it
// after the swap completes. Without this, two concurrent doSwap goroutines
// both compare against the same live reading and jointly overshoot — a big
// model loads over minutes while its memory climbs, and admissions during
// that window see only the partial footprint.
//
// In strict mode the gate is FAIL-CLOSED: a load that cannot fit (after LRU
// eviction and a bounded wait) is rejected with a MemoryBudgetError (HTTP 503).
// In non-strict mode it preserves the historical fail-open behavior: any
// condition it cannot resolve results in a warning and the load proceeding.
type memGate struct {
	budgetMB    int
	strict      bool
	hostFloorMB int
	maxWait     time.Duration
	redlineMB   int
	hardlineMB  int

	probe memProbe
	// liveRead, when non-nil, performs a fresh synchronous read of GPU memory
	// in use (never stale, unlike the polled probe). Production wires it to
	// perf.ReadLiveGpuMemUsedMB.
	liveRead func() (int, bool)
	// hostAvail, when non-nil, returns the host's available RAM in MiB. On
	// UMA systems the GPU and host share one pool, so the gate checks both.
	hostAvail func() (int, bool)

	// estimates maps model id -> configured VramEstimateMB, used to project a
	// load's footprint before it happens. Missing/zero means "unknown": the
	// gate behaves reactively for that model (non-strict) or rejects it
	// (strict).
	estimates map[string]int
	// stopTimeout bounds each synchronous victim Stop. Stopping a process frees
	// its GPU memory, so admission waits for it before re-checking the budget.
	stopTimeout time.Duration

	mu sync.Mutex
	// reservations maps model id -> reserved MiB for loads that have been
	// admitted but not yet Released. They count toward every projection and
	// their holders are never picked as eviction victims.
	reservations map[string]int
}

// newMemGate returns a gate, or nil when it should be disabled (no budget or no
// probe). A nil *memGate is safe: callers guard with `if b.memGate != nil`.
func newMemGate(budgetMB int, mon *perf.Monitor, estimates map[string]int, stopTimeout time.Duration) *memGate {
	if budgetMB <= 0 || mon == nil {
		return nil
	}
	return &memGate{
		budgetMB:     budgetMB,
		maxWait:      30 * time.Second,
		probe:        perfMemProbe{mon: mon},
		estimates:    estimates,
		stopTimeout:  stopTimeout,
		reservations: make(map[string]int),
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

	g := newMemGate(budget, mon, estimates, stopTimeout)
	g.strict = conf.Performance.GpuStrictAdmission
	g.hostFloorMB = conf.Performance.HostMemFloorMB
	if conf.Performance.AdmissionMaxWait > 0 {
		g.maxWait = conf.Performance.AdmissionMaxWait
	}
	g.redlineMB = conf.Performance.GpuRedlineMB
	g.hardlineMB = conf.Performance.GpuHardlineMB
	g.liveRead = perf.ReadLiveGpuMemUsedMB
	g.hostAvail = perf.ReadHostMemAvailableMB
	return g
}

// usedMB returns the freshest available GPU memory reading: a direct
// synchronous read when the platform supports it, otherwise the monitor's
// last polled sample.
func (g *memGate) usedMB() (int, bool) {
	if g.liveRead != nil {
		if used, ok := g.liveRead(); ok {
			return used, true
		}
	}
	if g.probe != nil {
		return g.probe.liveUsedMB()
	}
	return 0, false
}

// reservedMB sums all reservations except the named model's own.
func (g *memGate) reservedMB(excluding string) int {
	total := 0
	for id, mb := range g.reservations {
		if id == excluding {
			continue
		}
		total += mb
	}
	return total
}

// hostShortfall returns a non-empty description when loading `estimate` MiB
// would drop host available RAM below the configured floor. It returns "" when
// the check is disabled, unreadable, or passing.
func (g *memGate) hostShortfall(estimate int) string {
	if g.hostFloorMB <= 0 || g.hostAvail == nil {
		return ""
	}
	avail, ok := g.hostAvail()
	if !ok {
		return ""
	}
	if avail-estimate < g.hostFloorMB {
		return fmt.Sprintf("host MemAvailable %dMB - est %dMB < floor %dMB", avail, estimate, g.hostFloorMB)
	}
	return ""
}

// EnsureFits admits, or in strict mode rejects, the load of `incoming`. It
// serializes all admissions, projects `fresh live usage + in-flight
// reservations + incoming's estimate` against the budget (and host floor),
// evicts least-recently-used resident models synchronously to make room, and
// on success records a reservation the caller MUST Release once the swap
// completes (success or failure).
//
// Strict mode: when the load still cannot fit, waits up to maxWait
// (re-checking every admissionRetryInterval with the lock released) for
// memory to free — a TTL unload, another swap finishing — then returns a
// *MemoryBudgetError (rendered as HTTP 503 + Retry-After). Models without an
// estimate are rejected immediately: the gate cannot reason about them.
//
// Non-strict mode: preserves the historical FAIL-OPEN behavior. Any condition
// it cannot resolve (no reading, no victims, unknown-size victim) results in a
// warning and a nil return that lets the load proceed.
//
// incoming is the model about to be loaded (never evicted). alreadyEvicting are
// models the caller is already stopping for this swap (the solver's decision);
// they are excluded from victim selection and assumed already freed.
func (g *memGate) EnsureFits(
	incoming string,
	procs map[string]process.Process,
	alreadyEvicting []string,
	log *logmon.Monitor,
) error {
	if g == nil || g.budgetMB <= 0 {
		return nil
	}

	estimate := g.estimateMB(incoming)
	if g.strict && estimate <= 0 {
		return &MemoryBudgetError{
			Model:  incoming,
			Detail: "model has no vramEstimateMB configured; strict admission cannot project its footprint",
			// Not transient: config must change. Still 503 so clients treat
			// it as a load failure, but a long Retry-After discourages tight
			// retry loops.
			RetryAfter: 300,
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.reservations == nil {
		g.reservations = make(map[string]int)
	}

	deadline := time.Now().Add(g.maxWait)
	for {
		fits, detail := g.tryFit(incoming, estimate, procs, alreadyEvicting, log)
		if fits {
			if estimate > 0 {
				g.reservations[incoming] = estimate
			}
			return nil
		}
		if !g.strict {
			// tryFit already logged the specifics; fail open.
			return nil
		}
		if time.Now().After(deadline) {
			log.Warnf("memgate: rejecting %s after %s: %s", incoming, g.maxWait, detail)
			return &MemoryBudgetError{Model: incoming, Detail: detail, RetryAfter: 10}
		}
		log.Infof("memgate: %s does not fit yet (%s); waiting up to %s for memory to free",
			incoming, detail, time.Until(deadline).Round(time.Second))

		// Wait with the lock RELEASED so releases (finished swaps) and other
		// admissions that do fit are not blocked behind this one.
		g.mu.Unlock()
		time.Sleep(admissionRetryInterval)
		g.mu.Lock()
	}
}

// tryFit performs one admission attempt under g.mu: project, evict LRU models
// as needed, and report whether the load fits. On a false return, detail
// explains what blocked it. In non-strict mode unresolvable conditions are
// treated as fitting (fail-open), matching the historical behavior.
func (g *memGate) tryFit(
	incoming string,
	estimate int,
	procs map[string]process.Process,
	alreadyEvicting []string,
	log *logmon.Monitor,
) (bool, string) {
	used, ok := g.usedMB()
	if !ok {
		if !g.strict {
			// No reading available; fail open rather than blindly evicting.
			log.Debugf("memgate: no GPU reading available, skipping budget check for %s", incoming)
			return true, ""
		}
		return false, "no GPU memory reading available"
	}

	reserved := g.reservedMB(incoming)
	projected := used + reserved + estimate
	hostShort := g.hostShortfall(estimate)
	if projected <= g.budgetMB && hostShort == "" {
		log.Debugf("memgate: %s fits (used=%dMB + reserved=%dMB + est=%dMB = %dMB <= budget=%dMB)",
			incoming, used, reserved, estimate, projected, g.budgetMB)
		return true, ""
	}

	if hostShort != "" {
		log.Infof("memgate: %s blocked by host floor (%s), evicting LRU models", incoming, hostShort)
	} else {
		log.Infof("memgate: %s over budget (used=%dMB + reserved=%dMB + est=%dMB = %dMB > budget=%dMB), evicting LRU models",
			incoming, used, reserved, estimate, projected, g.budgetMB)
	}

	excluded := make(map[string]struct{}, len(alreadyEvicting)+len(g.reservations)+1)
	excluded[incoming] = struct{}{}
	for _, id := range alreadyEvicting {
		excluded[id] = struct{}{}
	}
	// Reservation holders are mid-load under another admission; evicting them
	// races that swap. A just-admitted model that has not started yet is also
	// still StateStopped with LastUse()==0, which would otherwise make it the
	// PREFERRED victim.
	for id := range g.reservations {
		excluded[id] = struct{}{}
	}

	for projected > g.budgetMB || hostShort != "" {
		victim, ok := g.pickLRUVictim(procs, excluded, false)
		if !ok {
			detail := fmt.Sprintf("projected=%dMB > budget=%dMB with no evictable models", projected, g.budgetMB)
			if hostShort != "" {
				detail = hostShort + " with no evictable models"
			}
			if !g.strict {
				log.Warnf("memgate: %s still over budget (%s); loading anyway", incoming, detail)
			}
			return false, detail
		}

		victimEst := g.estimateMB(victim)

		log.Infof("memgate: evicting LRU model %s to free memory for %s", victim, incoming)
		if err := procs[victim].Stop(g.stopTimeout); err != nil {
			log.Warnf("memgate: stopping %s failed: %v", victim, err)
		}
		excluded[victim] = struct{}{}

		used, ok = g.settleAfterEviction(used, victimEst)
		if !ok && victimEst <= 0 {
			// Unknown-size victim and no way to observe the freeing: we made
			// no measurable progress. Continuing would evict the entire
			// resident set on a single unknown model. Bound to this one
			// eviction.
			projected = used + g.reservedMB(incoming) + estimate
			if projected > g.budgetMB {
				detail := fmt.Sprintf("evicted unknown-size model %s but cannot confirm fit (projected=%dMB > budget=%dMB)",
					victim, projected, g.budgetMB)
				if !g.strict {
					log.Warnf("memgate: %s: %s; loading anyway", incoming, detail)
				}
				return false, detail
			}
			return true, ""
		}

		projected = used + g.reservedMB(incoming) + estimate
		hostShort = g.hostShortfall(estimate)
	}

	log.Infof("memgate: %s now fits (projected=%dMB <= budget=%dMB)", incoming, projected, g.budgetMB)
	return true, ""
}

// settleAfterEviction returns the best post-eviction usage figure. With a
// fresh reader available it polls (bounded by evictionSettleTimeout) until the
// reading drops below the pre-eviction value, i.e. the freeing was actually
// observed. Without one it falls back to arithmetic: the polled probe refreshes
// on a >=5s interval, so immediately after Stop it almost always still reports
// the pre-eviction value; trusting that stale reading would keep the projection
// over budget and evict the entire resident set. observed reports whether a
// real (non-arithmetic) drop was seen.
func (g *memGate) settleAfterEviction(preStopUsed, victimEst int) (used int, observed bool) {
	if g.liveRead != nil {
		deadline := time.Now().Add(evictionSettleTimeout)
		for {
			if fresh, ok := g.liveRead(); ok && fresh < preStopUsed {
				return fresh, true
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Arithmetic fallback: subtract the victim's estimated footprint and only
	// believe a probe reading when it reports LESS than the arithmetic
	// projection (i.e. it has actually observed the freeing, or other memory
	// was released too).
	used = preStopUsed - victimEst
	if used < 0 {
		used = 0
	}
	if g.probe != nil {
		if reUsed, ok := g.probe.liveUsedMB(); ok && reUsed < used {
			return reUsed, true
		}
	}
	return used, false
}

// Release drops the reservation recorded by a successful EnsureFits. Callers
// must invoke it once the swap completes (whether the load succeeded or
// failed): from that point the model's real footprint is visible to fresh
// reads, or gone.
func (g *memGate) Release(modelID string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.reservations, modelID)
	g.mu.Unlock()
}

// pickLRUVictim returns the resident (non-stopped) model with the oldest
// LastUse() that is not excluded. When spareMRU is true the single
// most-recently-used candidate is spared — the best available proxy for "the
// model actively serving right now". ok is false when no candidate exists.
func (g *memGate) pickLRUVictim(
	procs map[string]process.Process,
	excluded map[string]struct{},
	spareMRU bool,
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
		case process.StateStarting, process.StateStopping:
			// In transition under another (parallel) swap: Starting is a model
			// some other swap is mid-loading, Stopping is one already being
			// torn down. Evicting either races that swap. Skipping Starting
			// also fixes a subtler bug: a never-served model has LastUse()==0,
			// which sorts it FIRST, so a just-launched target would otherwise
			// be the preferred victim.
			continue
		}
		candidates = append(candidates, candidate{id: id, lastUse: p.LastUse()})
	}
	if len(candidates) == 0 {
		return "", false
	}
	if spareMRU && len(candidates) == 1 {
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
