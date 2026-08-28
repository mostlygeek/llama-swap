package router

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// groupRouting builds a normalized RoutingConfig for the group router, mirroring
// what config.LoadConfigFromReader produces. Tests use it to populate
// config.Config.Routing without going through LoadConfig.
func groupRouting(groups map[string]config.GroupConfig) config.RoutingConfig {
	return config.RoutingConfig{
		Router: config.RouterConfig{
			Use:      "group",
			Settings: config.RouterSettings{Groups: groups},
		},
	}
}

// fakeProcess is an in-memory implementation of process.Process used to drive
// the routers through their state machine without spawning real upstreams.
type fakeProcess struct {
	id string

	mu          sync.Mutex
	state       process.ProcessState
	readyCh     chan struct{}
	stopCh      chan struct{}
	runStarted  chan struct{} // closed on the first Run/EnsureReady call that starts
	stopStarted chan struct{} // closed on the first Stop call
	// ensureAsked is closed when EnsureReady is first called, before it
	// serializes against an in-flight Stop. Tests use it to observe that the
	// router asked for a start rather than deciding for itself not to.
	ensureAsked chan struct{}

	// opMu serializes Stop against EnsureReady the way ProcessCommand's run
	// loop does: a stop keeps that loop parked inside killProcess, so a start
	// request cannot even be received until the stop has finished. Without
	// this the fake would let a start decision be made against a stale state,
	// which is exactly the bug being guarded against (issue #946).
	opMu sync.Mutex

	autoReady bool

	// ensureErr, when non-nil, makes EnsureReady fail with it, driving the
	// dispatch-error path without needing a real process to die.
	ensureErr error

	// serveBlock, when non-nil, makes ServeHTTP receive from it before
	// writing its response. Tests use this to hold a request in-flight.
	// Closing the channel releases every blocked ServeHTTP caller.
	serveBlock chan struct{}
	// serveStarted is closed on the first ServeHTTP entry, letting tests
	// wait deterministically for the handler to begin executing.
	serveStarted chan struct{}
	// stopBlock, when non-nil, makes Stop receive from it (after signalling
	// stopStarted) before completing. Tests use this to prove that several
	// Stop calls can be in flight simultaneously.
	stopBlock chan struct{}

	runCalls     atomic.Int32
	stopCalls    atomic.Int32
	serveCalls   atomic.Int32
	stopTimeouts []time.Duration

	// onStop, when non-nil, is invoked at the start of every Stop call.
	// Tests share one recorder across fakes to observe stop ordering.
	onStop func(id string)

	// inFlightServe counts ServeHTTP calls currently inside the handler.
	// stoppedWhileServing flips true if Stop is ever called while that
	// counter is non-zero — a direct, race-free observation of the
	// "swap mid-request" anti-property.
	inFlightServe       atomic.Int32
	stoppedWhileServing atomic.Bool
}

func newFakeProcess(id string) *fakeProcess {
	return &fakeProcess{
		id:           id,
		state:        process.StateStopped,
		readyCh:      make(chan struct{}),
		stopCh:       make(chan struct{}),
		runStarted:   make(chan struct{}),
		stopStarted:  make(chan struct{}),
		ensureAsked:  make(chan struct{}),
		serveStarted: make(chan struct{}),
	}
}

func (f *fakeProcess) setState(s process.ProcessState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setStateLocked(s)
}

func (f *fakeProcess) setStateLocked(s process.ProcessState) {
	f.state = s
	switch s {
	case process.StateReady:
		select {
		case <-f.readyCh:
		default:
			close(f.readyCh)
		}
	case process.StateStopped:
		// A stopped process is no longer ready. Re-arm readyCh so a later
		// WaitReady/EnsureReady blocks until it is started again, mirroring
		// ProcessCommand — a latched-open readyCh would let a caller believe
		// a stopped process is serving.
		select {
		case <-f.readyCh:
			f.readyCh = make(chan struct{})
		default:
		}
	}
}

func (f *fakeProcess) State() process.ProcessState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeProcess) markReady() { f.setState(process.StateReady) }

func (f *fakeProcess) Run(_ time.Duration) error {
	f.runCalls.Add(1)
	f.mu.Lock()
	if f.state != process.StateStopped {
		s := f.state
		f.mu.Unlock()
		return fmt.Errorf("fakeProcess %s: Run called while %s", f.id, s)
	}
	f.state = process.StateStarting
	sc := f.stopCh
	select {
	case <-f.runStarted:
	default:
		close(f.runStarted)
	}
	f.mu.Unlock()

	if f.autoReady {
		f.setState(process.StateReady)
	}
	<-sc
	return nil
}

func (f *fakeProcess) Stop(timeout time.Duration) error {
	f.opMu.Lock()
	defer f.opMu.Unlock()

	f.stopCalls.Add(1)
	if f.onStop != nil {
		f.onStop(f.id)
	}
	if f.inFlightServe.Load() > 0 {
		f.stoppedWhileServing.Store(true)
	}
	f.mu.Lock()
	f.stopTimeouts = append(f.stopTimeouts, timeout)
	select {
	case <-f.stopStarted:
	default:
		close(f.stopStarted)
	}
	if f.state != process.StateStopped {
		f.setStateLocked(process.StateStopping)
	}
	f.mu.Unlock()

	// Test hook: hold Stop here so the test can prove multiple Stops are
	// in flight at the same time before any of them complete. While held the
	// process reports StateStopping, as a real one does.
	if f.stopBlock != nil {
		<-f.stopBlock
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == process.StateStopped {
		return nil
	}
	f.setStateLocked(process.StateStopped)
	select {
	case <-f.stopCh:
	default:
		close(f.stopCh)
	}
	return nil
}

func (f *fakeProcess) lastStopTimeout() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.stopTimeouts) == 0 {
		return 0
	}
	return f.stopTimeouts[len(f.stopTimeouts)-1]
}

// EnsureReady mirrors ProcessCommand.EnsureReady: it takes the start decision
// under opMu (the fake's stand-in for the run loop) so a stop still in flight
// holds the caller until the process is really stopped, then starts.
func (f *fakeProcess) EnsureReady(ctx context.Context, _ time.Duration) error {
	f.mu.Lock()
	select {
	case <-f.ensureAsked:
	default:
		close(f.ensureAsked)
	}
	f.mu.Unlock()

	f.opMu.Lock()
	f.mu.Lock()
	if f.ensureErr != nil {
		err := f.ensureErr
		f.mu.Unlock()
		f.opMu.Unlock()
		return err
	}
	switch f.state {
	case process.StateReady:
		f.mu.Unlock()
		f.opMu.Unlock()
		return nil
	case process.StateShutdown:
		f.mu.Unlock()
		f.opMu.Unlock()
		return fmt.Errorf("fakeProcess %s: shutdown", f.id)
	}
	// Counted as a run: EnsureReady is how the router starts a process now.
	f.runCalls.Add(1)
	f.setStateLocked(process.StateStarting)
	select {
	case <-f.runStarted:
	default:
		close(f.runStarted)
	}
	rc := f.readyCh
	f.mu.Unlock()
	f.opMu.Unlock()

	if f.autoReady {
		f.setState(process.StateReady)
	}
	select {
	case <-rc:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeProcess) WaitReady(ctx context.Context) error {
	f.mu.Lock()
	if f.state == process.StateReady {
		f.mu.Unlock()
		return nil
	}
	rc := f.readyCh
	f.mu.Unlock()
	select {
	case <-rc:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeProcess) Logger() *logmon.Monitor { return logmon.NewWriter(io.Discard) }

func (f *fakeProcess) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.serveCalls.Add(1)
	f.inFlightServe.Add(1)
	defer f.inFlightServe.Add(-1)
	f.mu.Lock()
	select {
	case <-f.serveStarted:
	default:
		close(f.serveStarted)
	}
	f.mu.Unlock()
	if f.serveBlock != nil {
		<-f.serveBlock
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok:%s", f.id)
}

// waitProcessed drains n events from ch, fataling on timeout. One event fires
// per handlerReq or swapDone fully absorbed by run().
func waitProcessed(t *testing.T, ch chan struct{}, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-t.Context().Done():
			t.Fatalf("waitProcessed: only %d/%d events received: %v", i, n, context.Cause(t.Context()))
		}
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-t.Context().Done():
		t.Fatalf("%s did not signal: %v", name, context.Cause(t.Context()))
	}
}

func newRequest(model string) *http.Request {
	body := fmt.Sprintf(`{"model":%q}`, model)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func newStreamRequest(model string) *http.Request {
	body := fmt.Sprintf(`{"model":%q,"stream":true}`, model)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func newRequestCtx(ctx context.Context, model string) *http.Request {
	return newRequest(model).WithContext(ctx)
}
