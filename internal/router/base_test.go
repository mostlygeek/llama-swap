package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/router/scheduler"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// These tests cover baseRouter's own machinery — the run loop, process
// lifecycle (doSwap), grant/ServeHTTP plumbing, Unload, and Shutdown. The
// scheduling decision logic (queueing, collation, eviction collisions) lives in
// the scheduler package and is tested directly there; see fifo_test.go.

// stubPlanner evicts configured targets. baseRouter tests drive the run loop
// through the default FIFO scheduler without exercising router planner details.
type stubPlanner struct {
	evict map[string][]string
}

func (s *stubPlanner) EvictionFor(target string, _ []string) []string {
	if s.evict == nil {
		return nil
	}
	return s.evict[target]
}
func (s *stubPlanner) OnSwapStart(string, []string) {}

func newTestBase(t *testing.T, processes map[string]process.Process, planner scheduler.Swapper) *baseRouter {
	t.Helper()
	conf := config.Config{HealthCheckTimeout: 5}
	return newTestBaseWithConfig(t, conf, processes, planner)
}

func newTestBaseWithConfig(t *testing.T, conf config.Config, processes map[string]process.Process, planner scheduler.Swapper) *baseRouter {
	t.Helper()
	b, err := newBaseRouter("test", conf, processes, logmon.NewWriter(io.Discard), planner)
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

func TestBaseRouter_RunningModels(t *testing.T) {
	ready := newFakeProcess("ready")
	ready.markReady()
	starting := newFakeProcess("starting")
	starting.setState(process.StateStarting)
	stopped := newFakeProcess("stopped")

	b := newTestBase(t, map[string]process.Process{
		"ready": ready, "starting": starting, "stopped": stopped,
	}, &stubPlanner{})

	running := b.RunningModels()
	if len(running) != 2 {
		t.Fatalf("running=%v want 2 entries", running)
	}
	if running["ready"] != process.StateReady {
		t.Errorf("ready state=%q want ready", running["ready"])
	}
	if running["starting"] != process.StateStarting {
		t.Errorf("starting state=%q want starting", running["starting"])
	}
	if _, ok := running["stopped"]; ok {
		t.Errorf("stopped process should be excluded from RunningModels")
	}
}

func TestBaseRouter_UnloadAll(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	c := newFakeProcess("c")
	c.markReady()

	b := newTestBase(t, map[string]process.Process{"a": a, "c": c}, &stubPlanner{})
	b.Unload(time.Second)

	if a.State() != process.StateStopped || c.State() != process.StateStopped {
		t.Fatalf("Unload() should stop every process: a=%q c=%q", a.State(), c.State())
	}
}

func TestBaseRouter_UnloadSpecificModel(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	c := newFakeProcess("c")
	c.markReady()

	b := newTestBase(t, map[string]process.Process{"a": a, "c": c}, &stubPlanner{})
	b.Unload(time.Second, "a")

	if a.State() != process.StateStopped {
		t.Errorf("a should be stopped, got %q", a.State())
	}
	if c.State() != process.StateReady {
		t.Errorf("c should remain ready, got %q", c.State())
	}
}

func TestBaseRouter_UnloadSpecificModelUsesConfiguredTimeout(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	c := newFakeProcess("c")
	c.markReady()

	conf := config.Config{
		HealthCheckTimeout: 5,
		UnloadTimeout:      25,
		Models: map[string]config.ModelConfig{
			"a": {UnloadTimeout: 45},
			"c": {UnloadTimeout: 25},
		},
	}
	b := newTestBaseWithConfig(t, conf, map[string]process.Process{"a": a, "c": c}, &stubPlanner{})
	b.Unload(0, "a")

	if a.lastStopTimeout() != 45*time.Second {
		t.Errorf("a stop timeout=%v want 45s", a.lastStopTimeout())
	}
	if got := c.stopCalls.Load(); got != 0 {
		t.Errorf("c stopCalls=%d want 0", got)
	}
}

func TestBaseRouter_UnloadAllUsesConfiguredTimeouts(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	c := newFakeProcess("c")
	c.markReady()

	conf := config.Config{
		HealthCheckTimeout: 5,
		UnloadTimeout:      25,
		Models: map[string]config.ModelConfig{
			"a": {UnloadTimeout: 45},
			"c": {UnloadTimeout: 25},
		},
	}
	b := newTestBaseWithConfig(t, conf, map[string]process.Process{"a": a, "c": c}, &stubPlanner{})
	b.Unload(0)

	if a.lastStopTimeout() != 45*time.Second {
		t.Errorf("a stop timeout=%v want 45s", a.lastStopTimeout())
	}
	if c.lastStopTimeout() != 25*time.Second {
		t.Errorf("c stop timeout=%v want 25s", c.lastStopTimeout())
	}
}

func TestBaseRouter_UnloadStopsSmallestTimeoutFirst(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	c := newFakeProcess("c")
	c.markReady()
	e := newFakeProcess("e")
	e.markReady()

	var mu sync.Mutex
	var order []string
	record := func(id string) {
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
	}
	a.onStop = record
	c.onStop = record
	e.onStop = record

	conf := config.Config{
		HealthCheckTimeout: 5,
		UnloadTimeout:      25,
		Models: map[string]config.ModelConfig{
			"a": {UnloadTimeout: 45},
			"c": {UnloadTimeout: 10},
			"e": {UnloadTimeout: 25},
		},
	}
	b := newTestBaseWithConfig(t, conf, map[string]process.Process{"a": a, "c": c, "e": e}, &stubPlanner{})
	// Named in descending timeout order; Unload must re-order ascending.
	b.Unload(0, "a", "e", "c")

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"c", "e", "a"}; !slices.Equal(order, want) {
		t.Errorf("stop order=%v want %v", order, want)
	}
}

// TestBaseRouter_UnloadZeroStopsSameTimeoutInParallel verifies that models
// resolving to the same unloadTimeout share one unload request and stop
// concurrently, rather than one request per model. Both fakeProcess.Stop
// calls are pinned via stopBlock; the test only releases them after
// observing both stopStarted, which deadlocks if the stops were sequential.
func TestBaseRouter_UnloadZeroStopsSameTimeoutInParallel(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	a.stopBlock = make(chan struct{})
	c := newFakeProcess("c")
	c.markReady()
	c.stopBlock = make(chan struct{})

	conf := config.Config{
		HealthCheckTimeout: 5,
		UnloadTimeout:      25,
		// no per-model values: both models inherit the global 25s
	}
	b := newTestBaseWithConfig(t, conf, map[string]process.Process{"a": a, "c": c}, &stubPlanner{})

	unloadDone := make(chan struct{})
	go func() {
		b.Unload(0)
		close(unloadDone)
	}()

	for _, p := range []*fakeProcess{a, c} {
		select {
		case <-p.stopStarted:
		case <-time.After(2 * time.Second):
			t.Fatalf("Stop on %s never started — same-timeout unloads are not parallel", p.id)
		}
	}
	close(a.stopBlock)
	close(c.stopBlock)

	select {
	case <-unloadDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Unload did not return after stops were released")
	}
	if a.lastStopTimeout() != 25*time.Second || c.lastStopTimeout() != 25*time.Second {
		t.Errorf("stop timeouts a=%v c=%v want 25s each", a.lastStopTimeout(), c.lastStopTimeout())
	}
}

func TestBaseRouter_UnloadPositiveTimeoutOverridesConfigured(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()

	conf := config.Config{
		HealthCheckTimeout: 5,
		UnloadTimeout:      25,
		Models: map[string]config.ModelConfig{
			"a": {UnloadTimeout: 45},
		},
	}
	b := newTestBaseWithConfig(t, conf, map[string]process.Process{"a": a}, &stubPlanner{})
	b.Unload(time.Second, "a")

	if a.lastStopTimeout() != time.Second {
		t.Errorf("a stop timeout=%v want 1s", a.lastStopTimeout())
	}
}

// TestBaseRouter_Unload_StopsInParallel verifies that Unload fans out its
// Stop calls concurrently rather than stopping each process serially. Each
// fakeProcess.Stop is pinned via stopBlock; the test only releases them
// after observing every stopStarted, proving all three Stops were in
// flight simultaneously.
func TestBaseRouter_Unload_StopsInParallel(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	a.stopBlock = make(chan struct{})
	pb := newFakeProcess("b")
	pb.markReady()
	pb.stopBlock = make(chan struct{})
	pc := newFakeProcess("c")
	pc.markReady()
	pc.stopBlock = make(chan struct{})

	b := newTestBase(t, map[string]process.Process{"a": a, "b": pb, "c": pc}, &stubPlanner{})

	unloadDone := make(chan struct{})
	go func() {
		b.Unload(time.Second, "a", "b", "c")
		close(unloadDone)
	}()

	// All three Stop calls must start before any of them are allowed to
	// complete. If Unload was serial, only one stopStarted would fire
	// until we released its stopBlock, and this would deadlock.
	for _, p := range []*fakeProcess{a, pb, pc} {
		select {
		case <-p.stopStarted:
		case <-time.After(2 * time.Second):
			t.Fatalf("Stop on %s never started — Unload is not parallel", p.id)
		}
	}

	// Release them; Unload should now return.
	close(a.stopBlock)
	close(pb.stopBlock)
	close(pc.stopBlock)

	select {
	case <-unloadDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Unload did not return after stops released")
	}

	for _, p := range []*fakeProcess{a, pb, pc} {
		if p.State() != process.StateStopped {
			t.Errorf("%s state=%q want stopped", p.id, p.State())
		}
		if got := p.stopCalls.Load(); got != 1 {
			t.Errorf("%s stopCalls=%d want 1", p.id, got)
		}
	}
}

func TestBaseRouter_OnDemandStart(t *testing.T) {
	a := newFakeProcess("a")
	a.autoReady = true

	b := newTestBase(t, map[string]process.Process{"a": a}, &stubPlanner{})

	w := httptest.NewRecorder()
	b.ServeHTTP(w, newRequest("a"))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.runCalls.Load(); got != 1 {
		t.Errorf("runCalls=%d want 1", got)
	}
	if got := a.serveCalls.Load(); got != 1 {
		t.Errorf("serveCalls=%d want 1", got)
	}
}

func TestBaseRouter_IgnoreWebsocketsRejectsModelUnlessReady(t *testing.T) {
	for _, state := range []process.ProcessState{process.StateStopped, process.StateStarting} {
		t.Run(string(state), func(t *testing.T) {
			a := newFakeProcess("a")
			if state != process.StateStopped {
				a.setState(state)
			}
			conf := config.Config{
				HealthCheckTimeout: 5,
				Models: map[string]config.ModelConfig{
					"a": {Compat: config.CompatConfig{IgnoreWebsockets: true}},
				},
			}
			b := newTestBaseWithConfig(t, conf, map[string]process.Process{"a": a}, &stubPlanner{})

			r := httptest.NewRequest(http.MethodGet, "/props?model=a", nil)
			r.Header.Set("Connection", "keep-alive, Upgrade")
			r.Header.Set("Upgrade", "websocket")
			w := httptest.NewRecorder()
			b.ServeHTTP(w, r)

			if w.Code != http.StatusConflict {
				t.Fatalf("status=%d want %d body=%q", w.Code, http.StatusConflict, w.Body.String())
			}
			if got := a.runCalls.Load(); got != 0 {
				t.Errorf("runCalls=%d want 0", got)
			}
			if got := a.serveCalls.Load(); got != 0 {
				t.Errorf("serveCalls=%d want 0", got)
			}
		})
	}
}

func TestBaseRouter_WebsocketStartsModelWhenCompatDisabled(t *testing.T) {
	a := newFakeProcess("a")
	a.autoReady = true
	conf := config.Config{
		HealthCheckTimeout: 5,
		Models:             map[string]config.ModelConfig{"a": {}},
	}
	b := newTestBaseWithConfig(t, conf, map[string]process.Process{"a": a}, &stubPlanner{})

	r := httptest.NewRequest(http.MethodGet, "/props?model=a", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want %d body=%q", w.Code, http.StatusOK, w.Body.String())
	}
	if got := a.runCalls.Load(); got != 1 {
		t.Errorf("runCalls=%d want 1", got)
	}
}

func TestBaseRouter_IgnoreWebsocketsDoesNotBlockSwap(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	a.serveBlock = make(chan struct{})
	pb := newFakeProcess("b")
	pb.autoReady = true
	conf := config.Config{
		HealthCheckTimeout: 5,
		Models: map[string]config.ModelConfig{
			"a": {Compat: config.CompatConfig{IgnoreWebsockets: true}},
			"b": {},
		},
	}
	b := newTestBaseWithConfig(t, conf, map[string]process.Process{"a": a, "b": pb}, &stubPlanner{
		evict: map[string][]string{"b": {"a"}},
	})

	websocketDone := make(chan struct{})
	go func() {
		defer close(websocketDone)
		r := httptest.NewRequest(http.MethodGet, "/props?model=a", nil)
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		b.ServeHTTP(httptest.NewRecorder(), r)
	}()
	waitSignal(t, a.serveStarted, "websocket request start")

	w := httptest.NewRecorder()
	b.ServeHTTP(w, newRequest("b"))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if !a.stoppedWhileServing.Load() {
		t.Fatal("ignored websocket prevented the conflicting model from swapping in")
	}

	close(a.serveBlock)
	waitSignal(t, websocketDone, "websocket request finish")
}

// TestBaseRouter_RequestDuringStop is the router-level regression test for
// issue #946. A process being stopped outside the router's knowledge (a TTL
// unload, a crash, an operator kill) must not wedge the swap machinery: the
// request has to wait for the stop to finish and then start the model.
//
// Before the fix doSwap read State(), saw StateStopping, skipped the start, and
// then subscribed to a process nobody would ever start — stranding the swap, so
// every later request for the model joined the same zombie swap and hung.
func TestBaseRouter_RequestDuringStop(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	a.autoReady = true
	// Pin Stop so the process sits in StateStopping while the request arrives.
	a.stopBlock = make(chan struct{})

	b := newTestBase(t, map[string]process.Process{"a": a}, &stubPlanner{})

	// Stop the process directly, the way the process's own TTL goroutine does —
	// the router is never told about it.
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		_ = a.Stop(time.Second)
	}()
	waitSignal(t, a.stopStarted, "a.stopStarted")

	if got := a.State(); got != process.StateStopping {
		t.Fatalf("State()=%s want %s before request", got, process.StateStopping)
	}

	w := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		defer close(served)
		b.ServeHTTP(w, newRequest("a"))
	}()

	// The router must ask the process to start even though it is mid-stop, and
	// leave the process to decide when. The stop is still pinned here, so this
	// signal can only arrive from a start requested during StateStopping —
	// which is precisely what the old State()-then-Run code refused to do.
	waitSignal(t, a.ensureAsked, "a.ensureAsked")

	// Let the unload complete. The request must now start the model itself.
	close(a.stopBlock)
	<-stopDone

	select {
	case <-served:
	case <-t.Context().Done():
		t.Fatalf("request during stop never completed: %v", context.Cause(t.Context()))
	}

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if got := a.runCalls.Load(); got != 1 {
		t.Errorf("runCalls=%d want 1 (model must be restarted after the unload)", got)
	}
	if got := a.serveCalls.Load(); got != 1 {
		t.Errorf("serveCalls=%d want 1", got)
	}
}

func TestBaseRouter_ContextCancel(t *testing.T) {
	a := newFakeProcess("a")
	// autoReady=false so swap parks forever until we mark ready.

	b := newTestBase(t, map[string]process.Process{"a": a}, &stubPlanner{})

	ctx, cancel := context.WithCancel(context.Background())
	w1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		b.ServeHTTP(w1, newRequestCtx(ctx, "a"))
		close(done1)
	}()

	w2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		b.ServeHTTP(w2, newRequest("a"))
		close(done2)
	}()

	waitProcessed(t, b.testProcessed, 2) // both requests joined the active swap
	<-a.runStarted

	cancel()
	select {
	case <-done1:
	case <-time.After(time.Second):
		t.Fatal("cancelled ServeHTTP did not return after ctx cancel")
	}

	a.markReady()
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("non-cancelled ServeHTTP did not complete after swap")
	}
	if w2.Code != http.StatusOK {
		t.Errorf("second request status=%d body=%q", w2.Code, w2.Body.String())
	}
}

func TestBaseRouter_ModelNotFound(t *testing.T) {
	a := newFakeProcess("a")
	b := newTestBase(t, map[string]process.Process{"a": a}, &stubPlanner{})

	w := httptest.NewRecorder()
	b.ServeHTTP(w, newRequest("unknown"))

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want %d body=%q", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestBaseRouter_ConcurrencyLimitRejectsBeforeLoadingStream(t *testing.T) {
	sendLoading := true
	conf := config.Config{
		HealthCheckTimeout: 5,
		Models: map[string]config.ModelConfig{
			"a": {ConcurrencyLimit: 2, SendLoadingState: &sendLoading},
			"b": {},
		},
	}
	a := newFakeProcess("a")
	a.autoReady = true
	bProc := newFakeProcess("b")
	bProc.autoReady = true
	bProc.serveBlock = make(chan struct{})

	b := newTestBaseWithConfig(t, conf, map[string]process.Process{"a": a, "b": bProc}, &stubPlanner{
		evict: map[string][]string{"a": {"b"}},
	})

	bDone := make(chan struct{})
	go func() {
		b.ServeHTTP(httptest.NewRecorder(), newStreamRequest("b"))
		close(bDone)
	}()
	waitSignal(t, bProc.serveStarted, "b request start")
	waitProcessed(t, b.testProcessed, 2)

	aDone1 := make(chan struct{})
	aDone2 := make(chan struct{})
	go func() {
		b.ServeHTTP(httptest.NewRecorder(), newStreamRequest("a"))
		close(aDone1)
	}()
	go func() {
		b.ServeHTTP(httptest.NewRecorder(), newStreamRequest("a"))
		close(aDone2)
	}()
	waitProcessed(t, b.testProcessed, 2)

	w := httptest.NewRecorder()
	b.ServeHTTP(w, newStreamRequest("a"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d want 429 body=%q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q want application/json", got)
	}
	if strings.Contains(w.Body.String(), "llama-swap loading model") {
		t.Fatalf("429 body contains loading stream: %q", w.Body.String())
	}
	// OpenAI clients read body["error"]["message"], so "error" must decode as
	// an object rather than a bare string.
	var envelope swaputil.ErrorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("429 body is not an OpenAI error envelope: %v, body=%q", err, w.Body.String())
	}
	if envelope.Error.Message == "" || envelope.Error.Type != swaputil.ErrorTypeRateLimit {
		t.Fatalf("429 error=%+v, want a rate_limit_error with a message", envelope.Error)
	}

	close(bProc.serveBlock)
	for name, ch := range map[string]chan struct{}{"b": bDone, "a1": aDone1, "a2": aDone2} {
		waitSignal(t, ch, name+" request finish")
	}
}

// TestBaseRouter_DispatchErrorFramedIntoLoadingStream covers the second half of
// #1029. Once the loading stream has committed its 200, an error can only reach
// the client in-band: swaputil.SendError's status is dropped and its JSON body
// lands as a bare line that every SSE parser discards, leaving the caller with
// a truncated stream, no [DONE], and no reason.
func TestBaseRouter_DispatchErrorFramedIntoLoadingStream(t *testing.T) {
	sendLoading := true
	conf := config.Config{
		HealthCheckTimeout: 5,
		Models:             map[string]config.ModelConfig{"a": {SendLoadingState: &sendLoading}},
	}
	a := newFakeProcess("a")
	a.ensureErr = fmt.Errorf("upstream command exited prematurely")

	b := newTestBaseWithConfig(t, conf, map[string]process.Process{"a": a}, &stubPlanner{})

	w := httptest.NewRecorder()
	b.ServeHTTP(w, newStreamRequest("a"))

	body := w.Body.String()
	// The loading text is streamed a few characters per frame, so reassemble it.
	if content := extractStreamedContent(body); !strings.Contains(content, "llama-swap loading model") {
		t.Fatalf("loading stream did not start, so this is not the path under test: %q", content)
	}
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, "data: ") {
			t.Errorf("line %q is not an SSE field; a client would silently ignore it", line)
		}
	}
	if !strings.Contains(body, "upstream command exited prematurely") {
		t.Errorf("dispatch error never reached the client: %q", body)
	}
	if !strings.HasSuffix(strings.TrimRight(body, "\n"), "data: [DONE]") {
		t.Errorf("stream not terminated with [DONE]: %q", body)
	}
}

func TestBaseRouter_Shutdown_StopsAllProcesses(t *testing.T) {
	a := newFakeProcess("a")
	a.markReady()
	go a.Run(0)
	pb := newFakeProcess("b")
	pb.markReady()
	go pb.Run(0)

	b := newTestBase(t, map[string]process.Process{"a": a, "b": pb}, &stubPlanner{})

	if err := b.Shutdown(time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := a.stopCalls.Load(); got != 1 {
		t.Errorf("a.stopCalls=%d want 1", got)
	}
	if got := pb.stopCalls.Load(); got != 1 {
		t.Errorf("b.stopCalls=%d want 1", got)
	}

	// Subsequent ServeHTTP should report 5xx.
	w := httptest.NewRecorder()
	b.ServeHTTP(w, newRequest("a"))
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusServiceUnavailable {
		t.Errorf("post-shutdown status=%d want 5xx body=%q", w.Code, w.Body.String())
	}

	// Second Shutdown should report already in progress.
	if err := b.Shutdown(0); err == nil {
		t.Errorf("second Shutdown returned nil, want error")
	}
}
