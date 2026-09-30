package router

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// fakeClock advances only when the test tells it to, so load durations are
// exact integers instead of wall-clock noise.
type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestTracker(t *testing.T, multipliers map[string]int) (*loadCostTracker, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	tracker := newLoadCostTracker(multipliers, logmon.NewWriter(io.Discard))
	tracker.now = clock.Now
	return tracker, clock
}

func observe(tracker *loadCostTracker, model, from, to string) {
	tracker.observe(swaputil.ProcessStateChangeEvent{
		ProcessName: model,
		OldState:    from,
		NewState:    to,
	})
}

// load models through one starting->ready cycle taking the given duration.
func loadModel(tracker *loadCostTracker, clock *fakeClock, model string, d time.Duration) {
	observe(tracker, model, "stopped", "starting")
	clock.Advance(d)
	observe(tracker, model, "starting", "ready")
}

func TestLoadCostTracker_observesStartingToReady(t *testing.T) {
	tracker, clock := newTestTracker(t, nil)

	loadModel(tracker, clock, "a", 2500*time.Millisecond)

	costs := tracker.EvictCosts([]string{"a"})
	if costs["a"] != 2500 {
		t.Errorf("cost a=%d want 2500", costs["a"])
	}
}

func TestLoadCostTracker_noSamplesFallsBackToOne(t *testing.T) {
	tracker, _ := newTestTracker(t, map[string]int{"slow": 5})

	costs := tracker.EvictCosts([]string{"slow", "plain"})
	if costs["slow"] != 5 {
		t.Errorf("cost slow=%d want 5 (multiplier with no data)", costs["slow"])
	}
	if costs["plain"] != 1 {
		t.Errorf("cost plain=%d want 1", costs["plain"])
	}
}

func TestLoadCostTracker_discardsFailedLoad(t *testing.T) {
	tracker, clock := newTestTracker(t, nil)

	// Load dies before reaching ready: no sample may be recorded.
	observe(tracker, "a", "stopped", "starting")
	clock.Advance(60 * time.Second)
	observe(tracker, "a", "starting", "stopped")

	// A later successful load must time from its own starting event.
	observe(tracker, "a", "stopped", "starting")
	clock.Advance(300 * time.Millisecond)
	observe(tracker, "a", "starting", "ready")

	costs := tracker.EvictCosts([]string{"a"})
	if costs["a"] != 300 {
		t.Errorf("cost a=%d want 300 (failed load must not contribute)", costs["a"])
	}
}

func TestLoadCostTracker_usesMedianOfSamples(t *testing.T) {
	tracker, clock := newTestTracker(t, nil)

	for _, d := range []time.Duration{1000, 9000, 2000, 8000, 3000} {
		loadModel(tracker, clock, "a", d*time.Millisecond)
	}
	// A lone model is its own fleet: shrinkage is a no-op, so the cost is
	// the plain median of the five samples.
	costs := tracker.EvictCosts([]string{"a"})
	if costs["a"] != 3000 {
		t.Errorf("cost a=%d want 3000 (median)", costs["a"])
	}
}

func TestLoadCostTracker_shrinksTowardFleetMedian(t *testing.T) {
	tracker, clock := newTestTracker(t, nil)

	for range 7 {
		loadModel(tracker, clock, "y", 200*time.Millisecond)
		loadModel(tracker, clock, "z", 300*time.Millisecond)
	}
	// One spike on a third model: with n=1 the estimate is
	// (fleet + spike) / 2 instead of the raw spike.
	loadModel(tracker, clock, "x", 10000*time.Millisecond)

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
	tracker, clock := newTestTracker(t, nil)

	for range loadSampleWindow {
		loadModel(tracker, clock, "a", 1000*time.Millisecond)
	}
	// Everything above the window is a slow era; the median must forget it.
	for range loadSampleWindow {
		loadModel(tracker, clock, "a", 50*time.Millisecond)
	}

	costs := tracker.EvictCosts([]string{"a"})
	if costs["a"] != 50 {
		t.Errorf("cost a=%d want 50 (stale samples evicted from window)", costs["a"])
	}
}

func TestLoadCostTracker_logsRecordedCost(t *testing.T) {
	var buf bytes.Buffer
	clock := &fakeClock{t: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	monitor := logmon.NewWriter(&buf)
	monitor.SetLogLevel(logmon.LevelDebug)
	tracker := newLoadCostTracker(nil, monitor)
	tracker.now = clock.Now

	loadModel(tracker, clock, "a", 2500*time.Millisecond)

	logged := buf.String()
	if !strings.Contains(logged, "loadcost") || !strings.Contains(logged, "model=a") ||
		!strings.Contains(logged, "load=2500ms") || !strings.Contains(logged, "cost=2500") {
		t.Errorf("debug log missing recorded cost, got: %q", logged)
	}
}

func TestLoadCostTracker_multiplierScalesEstimate(t *testing.T) {
	tracker, clock := newTestTracker(t, map[string]int{"a": 3})

	loadModel(tracker, clock, "a", 1000*time.Millisecond)

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

	tracker, clock := newTestTracker(t, matrix.ResolvedEvictCosts())
	loadModel(tracker, clock, "b", 5000*time.Millisecond)
	loadModel(tracker, clock, "c", 100*time.Millisecond)

	s := newMatrixSolver(matrix.Program(), tracker.EvictCosts)
	result := s.Solve("a", []string{"b", "c"})
	if result.SetName != "a_with_b" {
		t.Errorf("SetName=%q want a_with_b (slow b is pricier to evict)", result.SetName)
	}
	if len(result.Evict) != 1 || result.Evict[0] != "c" {
		t.Errorf("Evict=%v want [c]", result.Evict)
	}
}
