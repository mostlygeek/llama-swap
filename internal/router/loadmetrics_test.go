package router

import (
	"bytes"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

func newTestTracker(multipliers map[string]int) *loadCostTracker {
	return newLoadCostTracker(multipliers, logmon.NewWriter(io.Discard))
}

// loadModel delivers the exact event a process emits when a load of the
// given duration finishes: a starting->ready transition carrying the time
// genuinely spent in starting.
func loadModel(tracker *loadCostTracker, model string, d time.Duration) {
	tracker.observe(swaputil.ProcessStateChangeEvent{
		ProcessName: model,
		OldState:    "starting",
		NewState:    "ready",
		Elapsed:     d,
	})
}

func TestLoadCostTracker_observesStartingToReady(t *testing.T) {
	tracker := newTestTracker(nil)

	loadModel(tracker, "a", 2500*time.Millisecond)

	costs := tracker.EvictCosts([]string{"a"})
	if costs["a"] != 2500 {
		t.Errorf("cost a=%d want 2500", costs["a"])
	}
}

func TestLoadCostTracker_noSamplesFallsBackToOne(t *testing.T) {
	tracker := newTestTracker(map[string]int{"slow": 5})

	costs := tracker.EvictCosts([]string{"slow", "plain"})
	if costs["slow"] != 5 {
		t.Errorf("cost slow=%d want 5 (multiplier with no data)", costs["slow"])
	}
	if costs["plain"] != 1 {
		t.Errorf("cost plain=%d want 1", costs["plain"])
	}
}

func TestLoadCostTracker_ignoresOtherTransitions(t *testing.T) {
	tracker := newTestTracker(nil)

	// Transitions that are not starting->ready never contribute a load
	// sample: failing loads, unloads, and ready events whose previous state
	// was not starting (no Elapsed to trust).
	tracker.observe(swaputil.ProcessStateChangeEvent{
		ProcessName: "a", OldState: "starting", NewState: "stopped", Elapsed: 60 * time.Second,
	})
	tracker.observe(swaputil.ProcessStateChangeEvent{
		ProcessName: "a", OldState: "stopping", NewState: "ready", Elapsed: 300 * time.Millisecond,
	})
	tracker.observe(swaputil.ProcessStateChangeEvent{
		ProcessName: "a", OldState: "starting", NewState: "ready", Elapsed: 0,
	})

	if costs := tracker.EvictCosts([]string{"a"}); costs["a"] != 1 {
		t.Errorf("cost a=%d want 1 (no valid load samples)", costs["a"])
	}
}

func TestLoadCostTracker_usesEventElapsedNotDeliveryTime(t *testing.T) {
	tracker := newTestTracker(nil)

	// The event is handled long after the load finished; the measured
	// duration was captured by the emitter at transition time and is
	// immune to delivery delay.
	loadModel(tracker, "a", 2500*time.Millisecond)

	costs := tracker.EvictCosts([]string{"a"})
	if costs["a"] != 2500 {
		t.Errorf("cost a=%d want 2500 (event-carried duration)", costs["a"])
	}
}

func TestLoadCostTracker_usesMedianOfSamples(t *testing.T) {
	tracker := newTestTracker(nil)

	for _, ms := range []int64{1000, 9000, 2000, 8000, 3000} {
		loadModel(tracker, "a", time.Duration(ms)*time.Millisecond)
	}
	// A lone model is its own fleet: shrinkage is a no-op, so the cost is
	// the plain median of the five samples.
	costs := tracker.EvictCosts([]string{"a"})
	if costs["a"] != 3000 {
		t.Errorf("cost a=%d want 3000 (median)", costs["a"])
	}
}

func TestLoadCostTracker_shrinksTowardFleetMedian(t *testing.T) {
	tracker := newTestTracker(nil)

	for range 7 {
		loadModel(tracker, "y", 200*time.Millisecond)
		loadModel(tracker, "z", 300*time.Millisecond)
	}
	// One spike on a third model: with n=1 the estimate is
	// (fleet + spike) / 2 instead of the raw spike.
	loadModel(tracker, "x", 10000*time.Millisecond)

	// Own medians: y=200, z=300, x=10000 -> fleet median = 300.
	// est(x) = (300 + 10000) / 2 = 5150
	// est(y) = (300 + 7*200) / 8 = 212
	costs := tracker.EvictCosts([]string{"x", "y"})
	if costs["x"] != 5150 {
		t.Errorf("cost x=%d want 5150 (spike halved toward fleet)", costs["x"])
	}
	if costs["y"] != 212 {
		t.Errorf("cost y=%d want 212 (slightly pulled toward fleet)", costs["y"])
	}
}

func TestLoadCostTracker_windowKeepsLastSamples(t *testing.T) {
	tracker := newTestTracker(nil)

	for range loadSampleWindow {
		loadModel(tracker, "a", 1000*time.Millisecond)
	}
	// Everything above the window is a slow era; the median must forget it.
	for range loadSampleWindow {
		loadModel(tracker, "a", 50*time.Millisecond)
	}

	costs := tracker.EvictCosts([]string{"a"})
	if costs["a"] != 50 {
		t.Errorf("cost a=%d want 50 (stale samples evicted from window)", costs["a"])
	}
}

func TestLoadCostTracker_logsRecordedCost(t *testing.T) {
	var buf bytes.Buffer
	monitor := logmon.NewWriter(&buf)
	monitor.SetLogLevel(logmon.LevelDebug)
	tracker := newLoadCostTracker(nil, monitor)

	loadModel(tracker, "a", 2500*time.Millisecond)

	logged := buf.String()
	if !strings.Contains(logged, "loadcost") || !strings.Contains(logged, "model=a") ||
		!strings.Contains(logged, "load=2500ms") || !strings.Contains(logged, "cost=2500") {
		t.Errorf("debug log missing recorded cost, got: %q", logged)
	}
}

func TestLoadCostTracker_saturatesHugeCost(t *testing.T) {
	// A legal-but-absurd multiplier must not wrap the cost negative: the
	// solver's dominance logic requires positive eviction costs.
	tracker := newTestTracker(map[string]int{"a": math.MaxInt})

	loadModel(tracker, "a", 2*time.Millisecond)

	costs := tracker.EvictCosts([]string{"a"})
	if costs["a"] != math.MaxInt {
		t.Errorf("cost a=%d want %d (saturated, not wrapped)", costs["a"], math.MaxInt)
	}
}

func TestLoadCostTracker_multiplierScalesEstimate(t *testing.T) {
	tracker := newTestTracker(map[string]int{"a": 3})

	loadModel(tracker, "a", 1000*time.Millisecond)

	costs := tracker.EvictCosts([]string{"a"})
	if costs["a"] != 3000 {
		t.Errorf("cost a=%d want 3000 (3 x 1000)", costs["a"])
	}
}

// TestMatrixSolver_MeasuredLoadTimeDrivesEviction verifies the end-to-end
// shape: with no evict_costs configured, the model that takes longer to load
// is the one the solver prefers to keep.
func TestMatrixSolver_MeasuredLoadTimeDrivesEviction(t *testing.T) {
	models := map[string]config.ModelConfig{
		"a": {}, "b": {}, "c": {},
	}
	matrix := &config.MatrixConfig{
		Sets: config.OrderedSets{
			{Name: "a_with_c", DSL: "a & c"}, // evicts b
			{Name: "a_with_b", DSL: "a & b"}, // evicts c
		},
	}
	if err := config.ValidateMatrix(matrix, models); err != nil {
		t.Fatalf("ValidateMatrix: %v", err)
	}

	tracker := newTestTracker(matrix.ResolvedEvictCosts())
	loadModel(tracker, "b", 5000*time.Millisecond)
	loadModel(tracker, "c", 100*time.Millisecond)

	s := newMatrixSolver(matrix.Program(), tracker.EvictCosts)
	result := s.Solve("a", []string{"b", "c"})
	if result.SetName != "a_with_b" {
		t.Errorf("SetName=%q want a_with_b (slow b is pricier to evict)", result.SetName)
	}
	if len(result.Evict) != 1 || result.Evict[0] != "c" {
		t.Errorf("Evict=%v want [c]", result.Evict)
	}
}
