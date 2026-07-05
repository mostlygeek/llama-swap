package router

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

func newSerializeTestBase(t *testing.T, processes map[string]process.Process, priority map[string]int) *baseRouter {
	t.Helper()
	models := make(map[string]config.ModelConfig, len(processes))
	for id := range processes {
		models[id] = config.ModelConfig{}
	}
	conf := config.Config{
		HealthCheckTimeout: 5,
		Models:             models,
		Performance:        config.PerformanceConfig{SerializeInference: true},
		Routing: config.RoutingConfig{Scheduler: config.SchedulerConfig{Settings: config.SchedulerSettings{
			Fifo: config.FifoConfig{Priority: priority},
		}}},
	}
	b, err := newBaseRouter("test", conf, processes, logmon.NewWriter(io.Discard), &stubPlanner{}, nil)
	if err != nil {
		t.Fatalf("newBaseRouter: %v", err)
	}
	b.testProcessed = make(chan struct{}, 64)
	go b.run()
	t.Cleanup(func() {
		if !b.shuttingDown.Load() {
			_ = b.Shutdown(time.Second)
		}
	})
	return b
}

func TestMemGate_SkipsInFlightLRUVictim(t *testing.T) {
	perModel := map[string]int{"busy-lru": 5000, "idle": 5000}
	busy := readyProc("busy-lru", 100)
	idle := readyProc("idle", 200)
	procs := map[string]process.Process{"busy-lru": busy, "idle": idle}

	g := newTestGate(6000, nil, nil)
	g.probe = residentProbe(procs, perModel)
	g.inFlight = func(modelID string) int {
		if modelID == "busy-lru" {
			return 1
		}
		return 0
	}

	g.EnsureFits("incoming", procs, nil, gateLog())

	if busy.stopCalls.Load() != 0 {
		t.Fatalf("in-flight LRU model must not be evicted; stops=%d", busy.stopCalls.Load())
	}
	if idle.stopCalls.Load() != 1 {
		t.Fatalf("idle non-LRU should be evicted instead; stops=%d", idle.stopCalls.Load())
	}
}

func TestMemGate_HardlineCanEvictInFlightLRUVictim(t *testing.T) {
	busy := readyProc("busy-lru", 100)
	idle := readyProc("idle", 200)
	procs := map[string]process.Process{"busy-lru": busy, "idle": idle}
	g := newTestGate(1000, nil, nil)
	g.inFlight = func(modelID string) int {
		if modelID == "busy-lru" {
			return 1
		}
		return 0
	}

	victim, ok := g.pickLRUVictim(procs, nil, false, true)
	if !ok || victim != "busy-lru" {
		t.Fatalf("hardline selection should be able to pick in-flight LRU; victim=%q ok=%v", victim, ok)
	}
}

func TestBaseRouter_SerializeInference_OneAtATime(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	a.serveBlock = make(chan struct{})
	pb := newFakeProcess("b")
	pb.markReady()
	pb.serveBlock = make(chan struct{})
	b := newSerializeTestBase(t, map[string]process.Process{"a": a, "b": pb}, nil)

	doneA := make(chan struct{})
	go func() {
		b.ServeHTTP(httptest.NewRecorder(), newRequest("a"))
		close(doneA)
	}()
	<-a.serveStarted

	doneB := make(chan struct{})
	go func() {
		b.ServeHTTP(httptest.NewRecorder(), newRequest("b"))
		close(doneB)
	}()
	waitProcessed(t, b.testProcessed, 2)

	select {
	case <-pb.serveStarted:
		t.Fatal("second upstream handler started while first was still running")
	case <-time.After(100 * time.Millisecond):
	}

	close(a.serveBlock)
	select {
	case <-pb.serveStarted:
	case <-time.After(time.Second):
		t.Fatal("second upstream handler did not start after first released gate")
	}
	close(pb.serveBlock)
	<-doneA
	<-doneB
}

func TestBaseRouter_SerializeInference_PriorityOrdering(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	a.serveBlock = make(chan struct{})
	low := newFakeProcess("low")
	low.markReady()
	high := newFakeProcess("high")
	high.markReady()
	high.serveBlock = make(chan struct{})
	b := newSerializeTestBase(t, map[string]process.Process{"a": a, "low": low, "high": high}, map[string]int{"high": 10})

	go b.ServeHTTP(httptest.NewRecorder(), newRequest("a"))
	<-a.serveStarted

	lowDone := make(chan struct{})
	go func() { b.ServeHTTP(httptest.NewRecorder(), newRequest("low")); close(lowDone) }()
	waitProcessed(t, b.testProcessed, 2)
	highDone := make(chan struct{})
	go func() { b.ServeHTTP(httptest.NewRecorder(), newRequest("high")); close(highDone) }()
	waitProcessed(t, b.testProcessed, 1)
	// Let both granted HTTP goroutines reach the serialize gate before releasing
	// the running request; the gate orders the waiters present at release time.
	time.Sleep(50 * time.Millisecond)

	close(a.serveBlock)
	select {
	case <-high.serveStarted:
	case <-time.After(time.Second):
		t.Fatal("high-priority waiter did not start after release")
	}
	select {
	case <-low.serveStarted:
		t.Fatal("low-priority waiter started before high-priority waiter")
	default:
	}
	close(high.serveBlock)
	<-highDone
	select {
	case <-low.serveStarted:
	case <-time.After(time.Second):
		t.Fatal("low-priority waiter did not start after high-priority request completed")
	}
	<-lowDone
}

func TestBaseRouter_SerializeInference_ReleasesOnClientCancel(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	a.serveBlock = make(chan struct{})
	pb := newFakeProcess("b")
	pb.markReady()
	b := newSerializeTestBase(t, map[string]process.Process{"a": a, "b": pb}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	doneA := make(chan struct{})
	go func() { b.ServeHTTP(httptest.NewRecorder(), newRequestCtx(ctx, "a")); close(doneA) }()
	<-a.serveStarted

	cancel()
	close(a.serveBlock)
	select {
	case <-doneA:
	case <-time.After(time.Second):
		t.Fatal("cancelled request did not return")
	}

	doneB := make(chan struct{})
	go func() { b.ServeHTTP(httptest.NewRecorder(), newRequest("b")); close(doneB) }()
	select {
	case <-pb.serveStarted:
	case <-time.After(time.Second):
		t.Fatal("gate was not released after client-cancelled request returned")
	}
	<-doneB
}

type errorProcess struct {
	*fakeProcess
	inFlight *atomic.Int32
}

func (e *errorProcess) ServeHTTP(http.ResponseWriter, *http.Request) {
	e.serveCalls.Add(1)
	e.inFlight.Add(1)
	defer e.inFlight.Add(-1)
	close(e.serveStarted)
}

func TestBaseRouter_SerializeInference_ReleasesOnErrorReturn(t *testing.T) {
	var inFlight atomic.Int32
	errp := &errorProcess{fakeProcess: newFakeProcess("err"), inFlight: &inFlight}
	errp.markReady()
	ok := newFakeProcess("ok")
	ok.markReady()
	b := newSerializeTestBase(t, map[string]process.Process{"err": errp, "ok": ok}, nil)

	w := httptest.NewRecorder()
	b.ServeHTTP(w, newRequest("err"))
	if inFlight.Load() != 0 {
		t.Fatalf("erroring handler returned but test in-flight counter is %d", inFlight.Load())
	}

	done := make(chan struct{})
	go func() { b.ServeHTTP(httptest.NewRecorder(), newRequest("ok")); close(done) }()
	select {
	case <-ok.serveStarted:
	case <-time.After(time.Second):
		t.Fatal("gate was not released after upstream error/early return")
	}
	<-done
}

func (e *errorProcess) String() string { return fmt.Sprintf("errorProcess(%s)", e.id) }
