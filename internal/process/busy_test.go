package process

import (
	"context"
	"io"
	"math"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessCommand_ParseBusy(t *testing.T) {
	fields := []string{"queue_running", "queue_pending"}
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"idle", `{"queue_running":[],"queue_pending":[]}`, false},
		{"running job", `{"queue_running":[[1,"id"]],"queue_pending":[]}`, true},
		{"pending job", `{"queue_running":[],"queue_pending":[[2,"id"]]}`, true},
		{"missing fields", `{"other":[1]}`, false},
		{"non-zero number", `{"queue_running":2}`, true},
		{"zero number", `{"queue_running":0}`, false},
		{"true", `{"queue_pending":true}`, true},
		{"false", `{"queue_pending":false}`, false},
		{"null", `{"queue_running":null}`, false},
		{"non-empty object", `{"queue_running":{"a":1}}`, true},
		{"non-empty string", `{"queue_running":"x"}`, true},
		{"empty string", `{"queue_running":""}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBusy([]byte(tc.body), fields)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	_, err := parseBusy([]byte(`not json`), fields)
	assert.Error(t, err)
}

// busyActiveFlag reports the last busy check result, ignoring the hold period.
func (p *ProcessCommand) busyActiveFlag() bool {
	p.busyMu.Lock()
	defer p.busyMu.Unlock()
	return p.busyActive
}

func newBusyTestProcess(t *testing.T) *ProcessCommand {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p := &ProcessCommand{
		id: "busy-test",
		config: config.ModelConfig{BusyCheck: config.BusyCheckConfig{
			Endpoint: "/queue",
			Fields:   []string{"queue_running", "queue_pending"},
		}},
		parentCtx:   ctx,
		proxyLogger: logmon.NewWriter(io.Discard),
		busyKick:    make(chan struct{}, 1),
	}
	p.status.Store(&Status{State: StateReady})
	return p
}

// TestProcessCommand_BusyCheckTracksUpstream verifies the poller follows what
// the upstream reports, probes the configured endpoint, and goes back to idle
// when the process leaves the ready state.
func TestProcessCommand_BusyCheckTracksUpstream(t *testing.T) {
	var running atomic.Bool
	var path atomic.Value
	handler := func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		if running.Load() {
			_, _ = w.Write([]byte(`{"queue_running":[[1,"job"]],"queue_pending":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"queue_running":[],"queue_pending":[]}`))
	}

	p := newBusyTestProcess(t)
	p.startBusyPolling(handler)

	assert.Eventually(t, func() bool { return path.Load() == "/queue" }, 3*time.Second, 10*time.Millisecond)
	assert.False(t, p.Busy())

	// A job shows up right after a request finishes: the kick makes the
	// poller notice immediately rather than on the next tick.
	running.Store(true)
	p.kickBusyCheck()
	assert.Eventually(t, p.Busy, time.Second, 10*time.Millisecond)

	running.Store(false)
	p.kickBusyCheck()
	assert.Eventually(t, func() bool { return !p.Busy() }, time.Second, 10*time.Millisecond)

	running.Store(true)
	p.kickBusyCheck()
	assert.Eventually(t, p.Busy, time.Second, 10*time.Millisecond)

	// Leaving the ready state ends polling and clears the busy flag.
	p.status.Store(&Status{State: StateStopping})
	p.kickBusyCheck()
	assert.Eventually(t, func() bool {
		p.busyMu.Lock()
		defer p.busyMu.Unlock()
		return !p.busyPolling
	}, 3*time.Second, 10*time.Millisecond, "poller did not stop after the process left StateReady")
	assert.False(t, p.Busy())
}

// TestProcessCommand_BusyCheckFailuresReleaseModel verifies a busy check that
// stops answering cannot keep a model busy forever.
func TestProcessCommand_BusyCheckFailuresReleaseModel(t *testing.T) {
	var fail atomic.Bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"queue_running":[[1,"job"]]}`))
	}

	p := newBusyTestProcess(t)
	p.startBusyPolling(handler)

	assert.Eventually(t, p.Busy, time.Second, 10*time.Millisecond)

	// The last known state survives a few failed probes ...
	fail.Store(true)
	for i := 0; i < busyMaxProbeFailures; i++ {
		p.kickBusyCheck()
		time.Sleep(20 * time.Millisecond)
	}
	assert.True(t, p.Busy())

	// ... but not an endless run of them.
	for i := 0; i < 5; i++ {
		p.kickBusyCheck()
		time.Sleep(20 * time.Millisecond)
	}
	assert.Eventually(t, func() bool { return !p.Busy() }, 2*time.Second, 20*time.Millisecond)
}

func TestProcessCommand_BusyIsFalseWithoutBusyCheck(t *testing.T) {
	p := &ProcessCommand{busyKick: make(chan struct{}, 1)}
	p.kickBusyCheck() // must not block or panic
	assert.False(t, p.Busy())
}

// TestProcessCommand_BusyCheckHold verifies the model keeps counting as busy
// for busyCheck.hold seconds after the upstream reports idle, so clients can
// fetch the result of a finished job, and that new work cancels the hold.
func TestProcessCommand_BusyCheckHold(t *testing.T) {
	var running atomic.Bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		if running.Load() {
			_, _ = w.Write([]byte(`{"queue_running":[[1,"job"]],"queue_pending":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"queue_running":[],"queue_pending":[]}`))
	}

	p := newBusyTestProcess(t)
	p.config.BusyCheck.Hold = 2
	p.startBusyPolling(handler)

	running.Store(true)
	p.kickBusyCheck()
	assert.Eventually(t, p.Busy, time.Second, 10*time.Millisecond)

	// The job finishes: the flag drops but Busy() stays true during the hold.
	running.Store(false)
	p.kickBusyCheck()
	assert.Eventually(t, func() bool { return !p.busyActiveFlag() }, time.Second, 10*time.Millisecond)
	assert.True(t, p.Busy(), "must stay busy during the hold")

	// A new job during the hold ends it and the flag takes over again.
	running.Store(true)
	p.kickBusyCheck()
	assert.Eventually(t, func() bool { return p.busyActiveFlag() }, time.Second, 10*time.Millisecond)
	assert.Equal(t, int64(math.MaxInt64), p.busyUntil.Load())

	// Without new work the hold runs out.
	running.Store(false)
	p.kickBusyCheck()
	assert.Eventually(t, func() bool { return !p.busyActiveFlag() }, time.Second, 10*time.Millisecond)
	assert.True(t, p.Busy())
	assert.Eventually(t, func() bool { return !p.Busy() }, 4*time.Second, 50*time.Millisecond)
}

// TestProcessCommand_BusyCheckNoHold verifies Busy() drops right away when no
// hold is configured.
func TestProcessCommand_BusyCheckNoHold(t *testing.T) {
	var running atomic.Bool
	running.Store(true)
	handler := func(w http.ResponseWriter, r *http.Request) {
		if running.Load() {
			_, _ = w.Write([]byte(`{"queue_running":[[1,"job"]]}`))
			return
		}
		_, _ = w.Write([]byte(`{"queue_running":[]}`))
	}
	p := newBusyTestProcess(t)
	p.startBusyPolling(handler)
	assert.Eventually(t, p.Busy, time.Second, 10*time.Millisecond)

	running.Store(false)
	p.kickBusyCheck()
	assert.Eventually(t, func() bool { return !p.Busy() }, time.Second, 10*time.Millisecond)
}

// TestProcessCommand_BusyStaysTrueWhileProbePending verifies that a finished
// request keeps the model busy until the probe that follows it completes. The
// request may have queued a job the cached result does not show yet, and a
// swap in that window would kill it.
func TestProcessCommand_BusyStaysTrueWhileProbePending(t *testing.T) {
	var running, slow atomic.Bool
	var probes atomic.Int32
	gate := make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		if slow.Load() {
			<-gate
		}
		if running.Load() {
			_, _ = w.Write([]byte(`{"queue_running":[[1,"job"]]}`))
			return
		}
		_, _ = w.Write([]byte(`{"queue_running":[]}`))
	}

	p := newBusyTestProcess(t)
	p.startBusyPolling(handler)
	assert.Eventually(t, func() bool { return probes.Load() >= 1 }, time.Second, 5*time.Millisecond)
	assert.False(t, p.Busy())

	// A request finishes and left a job behind, but the probe is still running.
	slow.Store(true)
	running.Store(true)
	p.kickBusyCheck()
	assert.False(t, p.busyActiveFlag(), "the cached result does not show the job yet")
	assert.True(t, p.Busy(), "must stay busy until the probe completes")
	assert.Never(t, func() bool { return !p.Busy() }, 200*time.Millisecond, 10*time.Millisecond)

	close(gate)
	assert.Eventually(t, p.busyActiveFlag, time.Second, 10*time.Millisecond)

	// With nothing left behind the pending marker clears and Busy() drops.
	running.Store(false)
	p.kickBusyCheck()
	assert.Eventually(t, func() bool { return !p.Busy() }, time.Second, 10*time.Millisecond)
}

// TestProcessCommand_BusyHoldHasNoIdleGap verifies readers never see Busy()
// return false while the poller moves between busy, hold and busy again.
func TestProcessCommand_BusyHoldHasNoIdleGap(t *testing.T) {
	p := newBusyTestProcess(t)
	p.config.BusyCheck.Hold = 60

	p.busyMu.Lock()
	p.busyPolling = true
	p.setBusyLocked(true)
	p.busyMu.Unlock()

	var idle atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if !p.Busy() {
				idle.Add(1)
			}
		}
	}()

	for i := 0; i < 3000; i++ {
		p.busyMu.Lock()
		p.setBusyLocked(false) // idle, hold takes over
		p.setBusyLocked(true)  // new job during the hold
		p.busyMu.Unlock()
	}
	close(stop)
	<-done
	assert.Zero(t, idle.Load(), "Busy() returned false during a transition")
}

// TestProcessCommand_BusyPollerFromOldStartIsIgnored verifies a poller left
// over from an earlier start cannot overwrite the state of a newer one, even
// if its probe only returns after the restart.
func TestProcessCommand_BusyPollerFromOldStartIsIgnored(t *testing.T) {
	gate := make(chan struct{})
	oldHandler := func(w http.ResponseWriter, r *http.Request) {
		<-gate // the old probe is still running when the process restarts
		_, _ = w.Write([]byte(`{"queue_running":[[1,"old job"]]}`))
	}
	newHandler := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"queue_running":[]}`))
	}

	p := newBusyTestProcess(t)
	p.startBusyPolling(oldHandler)
	p.startBusyPolling(newHandler) // restart: new generation, state reset

	close(gate) // the old probe now returns "busy"
	assert.Never(t, p.Busy, 1500*time.Millisecond, 10*time.Millisecond,
		"result of a poller from an earlier start leaked into the new one")
}
