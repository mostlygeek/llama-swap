package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/config"
)

// newTestGroup builds a Group directly from the supplied processes and config,
// bypassing NewGroup's call to process.New.
func newTestGroup(t *testing.T, conf config.Config, processes map[string]process.Process) *Group {
	t.Helper()
	modelToGroup := make(map[string]string)
	for gid, gcfg := range conf.Groups {
		for _, mid := range gcfg.Members {
			modelToGroup[mid] = gid
		}
	}
	planner := &groupPlanner{
		config:       conf,
		modelToGroup: modelToGroup,
		processes:    processes,
	}
	base := newBaseRouter("group", conf, processes, planner, logmon.NewWriter(io.Discard))
	base.testProcessed = make(chan struct{}, 64)
	g := &Group{baseRouter: base}
	go base.run()
	t.Cleanup(func() {
		if !g.shuttingDown.Load() {
			_ = g.Shutdown(time.Second)
		}
	})
	return g
}

func TestGroup_NewGroup_DuplicateMembership(t *testing.T) {
	conf := config.Config{
		Groups: map[string]config.GroupConfig{
			"g1": {Swap: true, Members: []string{"a"}},
			"g2": {Swap: true, Members: []string{"a"}},
		},
		Models: map[string]config.ModelConfig{
			"a": {},
		},
	}
	log := logmon.NewWriter(io.Discard)
	if _, err := NewGroup(conf, log, log); err == nil {
		t.Fatalf("expected error for duplicate membership")
	}
}

func TestGroup_ServeHTTP_SwapStopsPrevious(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0) // park a Run goroutine so Stop has something to release

	b := newFakeProcess("b")
	b.autoReady = true

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: true, Exclusive: true, Members: []string{"a", "b"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("b"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1", got)
	}
	if got := b.runCalls.Load(); got != 1 {
		t.Errorf("b.runCalls=%d want 1", got)
	}
	if got := b.serveCalls.Load(); got != 1 {
		t.Errorf("b.serveCalls=%d want 1", got)
	}
}

func TestGroup_NonSwapGroup_NoStop(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()

	b := newFakeProcess("b")
	b.autoReady = true

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g": {Swap: false, Exclusive: false, Members: []string{"a", "b"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("b"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.stopCalls.Load(); got != 0 {
		t.Errorf("a.stopCalls=%d want 0 (swap=false should not stop siblings)", got)
	}
	if got := b.runCalls.Load(); got != 1 {
		t.Errorf("b.runCalls=%d want 1", got)
	}
}

func TestGroup_CrossGroupExclusive(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0)

	b := newFakeProcess("b")
	b.autoReady = true

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g1": {Swap: true, Exclusive: true, Members: []string{"a"}},
			"g2": {Swap: true, Exclusive: true, Members: []string{"b"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("b"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1 (cross-group exclusive must stop)", got)
	}
}

// TestGroup_NonExclusiveDoesNotUnloadExclusive pins a backwards-compatible
// gotcha from the original ProcessGroup: when a model in a non-exclusive group
// is loaded, any running exclusive group keeps running. The two coexist.
func TestGroup_NonExclusiveDoesNotUnloadExclusive(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0)

	b := newFakeProcess("b")
	b.autoReady = true

	conf := config.Config{
		HealthCheckTimeout: 5,
		Groups: map[string]config.GroupConfig{
			"g1": {Swap: true, Exclusive: true, Members: []string{"a"}},
			"g2": {Swap: true, Exclusive: false, Members: []string{"b"}},
		},
	}
	g := newTestGroup(t, conf, map[string]process.Process{"a": a, "b": b})

	w := httptest.NewRecorder()
	g.ServeHTTP(w, newRequest("b"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.stopCalls.Load(); got != 0 {
		t.Errorf("a.stopCalls=%d want 0 (non-exclusive target must not unload exclusive group)", got)
	}
	if a.State() != process.StateStarting && a.State() != process.StateReady {
		t.Errorf("a state=%s want still running", a.State())
	}
	if got := b.runCalls.Load(); got != 1 {
		t.Errorf("b.runCalls=%d want 1", got)
	}
}
