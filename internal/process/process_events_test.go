package process

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

func TestProcessCommand_EmitsStateChangeEvents(t *testing.T) {
	skipIfNoSimpleResponder(t)

	var mu sync.Mutex
	var transitions []swaputil.ProcessStateChangeEvent
	cancel := event.On(func(e swaputil.ProcessStateChangeEvent) {
		if e.ProcessName != t.Name() {
			return
		}
		mu.Lock()
		transitions = append(transitions, e)
		mu.Unlock()
	})
	defer cancel()

	cmd, port := simpleResponderCmd(t, "-silent", "-respond hello")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})

	runErr := runAsync(t, p)
	if err := p.Stop(testStopTimeout); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-runErr

	// Events are delivered asynchronously; give the dispatcher a moment.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(transitions)
		mu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(testPollInterval)
	}

	mu.Lock()
	defer mu.Unlock()

	for _, e := range transitions {
		if e.OldState == e.NewState {
			t.Errorf("emitted no-op transition: %s -> %s", e.OldState, e.NewState)
		}
	}

	want := []string{
		string(StateStopped) + "->" + string(StateStarting),
		string(StateStarting) + "->" + string(StateReady),
		string(StateReady) + "->" + string(StateStopping),
		string(StateStopping) + "->" + string(StateStopped),
	}
	got := make([]string, len(transitions))
	for i, e := range transitions {
		got[i] = e.OldState + "->" + e.NewState
	}
	if len(got) != len(want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("transitions = %v, want %v", got, want)
		}
	}
}

func TestProcessCommand_ReadySince(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent", "-respond hello")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})

	if got := p.ReadySince(); !got.IsZero() {
		t.Fatalf("ReadySince before start = %v, want zero", got)
	}

	before := time.Now()
	runErr := runAsync(t, p)
	got := p.ReadySince()
	if got.Before(before) || got.After(time.Now()) {
		t.Errorf("ReadySince = %v, want between %v and now", got, before)
	}

	if err := p.Stop(testStopTimeout); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-runErr
	if got := p.ReadySince(); !got.IsZero() {
		t.Errorf("ReadySince after stop = %v, want zero", got)
	}
}
