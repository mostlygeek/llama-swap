package process

import (
	"context"
	"io"
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
	done := make(chan struct{})
	go func() {
		p.pollBusy(handler)
		close(done)
	}()

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
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("poller did not stop after the process left StateReady")
	}
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
	go p.pollBusy(handler)

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
	go p.pollBusy(handler)

	running.Store(true)
	p.kickBusyCheck()
	assert.Eventually(t, p.Busy, time.Second, 10*time.Millisecond)

	// The job finishes: the flag drops but Busy() stays true during the hold.
	running.Store(false)
	p.kickBusyCheck()
	assert.Eventually(t, func() bool { return !p.busy.Load() }, time.Second, 10*time.Millisecond)
	assert.True(t, p.Busy(), "must stay busy during the hold")

	// A new job during the hold ends it and the flag takes over again.
	running.Store(true)
	p.kickBusyCheck()
	assert.Eventually(t, func() bool { return p.busy.Load() }, time.Second, 10*time.Millisecond)
	assert.Equal(t, int64(0), p.busyHoldUntil.Load())

	// Without new work the hold runs out.
	running.Store(false)
	p.kickBusyCheck()
	assert.Eventually(t, func() bool { return !p.busy.Load() }, time.Second, 10*time.Millisecond)
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
	go p.pollBusy(handler)
	assert.Eventually(t, p.Busy, time.Second, 10*time.Millisecond)

	running.Store(false)
	p.kickBusyCheck()
	assert.Eventually(t, func() bool { return !p.Busy() }, time.Second, 10*time.Millisecond)
}
