package router

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// loadSampleWindow is how many recent load durations each model keeps for
// its median estimate. Small on purpose: loads are sparse and the median of
// a short window absorbs the occasional contention spike without carrying
// stale hardware conditions for long.
const loadSampleWindow = 7

// loadCostTracker estimates what it costs to recover each model (its
// measured median load duration in milliseconds) and multiplies it by the
// configured evict_costs multiplier. The result feeds the matrix solver's
// eviction costs, so "expensive to reload" is measured instead of guessed.
//
// Models with no samples shrink toward the fleet median (the median of every
// measured model's own median) so a single early spike cannot define a
// model's cost: est = (fleet + n*own) / (n+1). With nothing measured at all
// the estimate is 1, which reproduces the legacy flat default cost.
//
// observe runs on the event dispatcher's goroutine; EvictCosts runs on the
// scheduler goroutine. The mutex guards both.
type loadCostTracker struct {
	mu          sync.Mutex
	multipliers map[string]int       // model -> configured evict_costs multiplier
	pending     map[string]time.Time // process -> when it last entered starting
	samples     map[string][]int64   // model -> recent load durations in ms
	logger      *logmon.Monitor      // debug trail for recorded costs; may be nil
	now         func() time.Time     // clock seam for tests
}

func newLoadCostTracker(multipliers map[string]int, logger *logmon.Monitor) *loadCostTracker {
	return &loadCostTracker{
		multipliers: multipliers,
		pending:     make(map[string]time.Time),
		samples:     make(map[string][]int64),
		logger:      logger,
		now:         time.Now,
	}
}

// attach subscribes the tracker to process lifecycle events on the default
// dispatcher and unsubscribes when ctx is cancelled.
func (t *loadCostTracker) attach(ctx context.Context) {
	cancel := event.On(func(e swaputil.ProcessStateChangeEvent) {
		t.observe(e)
	})
	go func() {
		<-ctx.Done()
		cancel()
	}()
}

// observe records a load sample for a starting->ready transition. Loads that
// never reach ready (failed, aborted, stopped mid-start) leave no sample.
func (t *loadCostTracker) observe(e swaputil.ProcessStateChangeEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch e.NewState {
	case string(process.StateStarting):
		t.pending[e.ProcessName] = t.now()
	case string(process.StateReady):
		if start, ok := t.pending[e.ProcessName]; ok {
			elapsed := t.now().Sub(start).Milliseconds()
			samples := append(t.samples[e.ProcessName], elapsed)
			if len(samples) > loadSampleWindow {
				samples = samples[len(samples)-loadSampleWindow:]
			}
			t.samples[e.ProcessName] = samples
			delete(t.pending, e.ProcessName)
			if t.logger != nil {
				estimate := t.estimateLocked(e.ProcessName, t.fleetMedianLocked())
				t.logger.Debugf("matrix: loadcost model=%s load=%dms samples=%d cost=%d",
					e.ProcessName, elapsed, len(samples),
					t.multiplierLocked(e.ProcessName)*estimate)
			}
		}
	default:
		delete(t.pending, e.ProcessName)
	}
}

// EvictCosts builds the solver's eviction cost map for the given running
// models: multiplier * max(estimate, 1), where the estimate is the shrunk
// median load duration in milliseconds.
func (t *loadCostTracker) EvictCosts(running []string) map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()

	fleet := t.fleetMedianLocked()
	costs := make(map[string]int, len(running))
	for _, model := range running {
		costs[model] = t.multiplierLocked(model) * t.estimateLocked(model, fleet)
	}
	return costs
}

func (t *loadCostTracker) multiplierLocked(model string) int {
	if mult, ok := t.multipliers[model]; ok {
		return mult
	}
	return 1
}

// estimateLocked returns the shrunk median estimate for a model, floored at
// 1 so an instant load still yields a positive eviction cost.
func (t *loadCostTracker) estimateLocked(model string, fleetMedian int64) int {
	samples := t.samples[model]
	n := len(samples)
	if n == 0 {
		if fleetMedian < 1 {
			return 1
		}
		return int(fleetMedian)
	}
	own := medianOf(samples)
	if own < 1 {
		own = 1
	}
	// w*fleet + (1-w)*own with w = 1/(n+1), in integer arithmetic.
	est := (fleetMedian + int64(n)*own) / int64(n+1)
	if est < 1 {
		return 1
	}
	return int(est)
}

// fleetMedianLocked is the median of every measured model's own sample
// median. Models with no samples are excluded, so the fleet only reflects
// observed data.
func (t *loadCostTracker) fleetMedianLocked() int64 {
	medians := make([]int64, 0, len(t.samples))
	for _, samples := range t.samples {
		if len(samples) == 0 {
			continue
		}
		medians = append(medians, medianOf(samples))
	}
	return medianOf(medians)
}

func medianOf(samples []int64) int64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
