package router

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/process"
)

// fakeProcess is an in-memory implementation of process.Process used to drive
// the routers through their state machine without spawning real upstreams.
type fakeProcess struct {
	id string

	mu         sync.Mutex
	state      process.ProcessState
	readyCh    chan struct{}
	stopCh     chan struct{}
	runStarted chan struct{} // closed on the first Run call

	autoReady bool

	runCalls   atomic.Int32
	stopCalls  atomic.Int32
	serveCalls atomic.Int32
}

func newFakeProcess(id string) *fakeProcess {
	return &fakeProcess{
		id:         id,
		state:      process.StateStopped,
		readyCh:    make(chan struct{}),
		stopCh:     make(chan struct{}),
		runStarted: make(chan struct{}),
	}
}

func (f *fakeProcess) setState(s process.ProcessState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = s
	if s == process.StateReady {
		select {
		case <-f.readyCh:
		default:
			close(f.readyCh)
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

func (f *fakeProcess) Stop(_ time.Duration) error {
	f.stopCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == process.StateStopped {
		return nil
	}
	f.state = process.StateStopped
	select {
	case <-f.stopCh:
	default:
		close(f.stopCh)
	}
	return nil
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

func (f *fakeProcess) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.serveCalls.Add(1)
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
		case <-time.After(2 * time.Second):
			t.Fatalf("waitProcessed: only %d/%d events received", i, n)
		}
	}
}

func newRequest(model string) *http.Request {
	body := fmt.Sprintf(`{"model":%q}`, model)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func newRequestCtx(ctx context.Context, model string) *http.Request {
	return newRequest(model).WithContext(ctx)
}
