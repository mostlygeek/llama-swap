package process

import (
	"fmt"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// TestProcessCommand_TTLStartsWhenProcessBecomesReady verifies that a freshly
// started/preloaded process receives a full TTL window even if no inference
// request has completed yet. lastUse starts at zero, so using it as the idle
// baseline would make the first one-second TTL tick unload any positive-TTL
// process immediately.
func TestProcessCommand_TTLStartsWhenProcessBecomesReady(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
		UnloadAfter:        5,
		UnloadTimeout:      1,
	})

	runErr := runAsync(t, p)
	defer func() {
		if p.State() == StateReady {
			_ = p.Stop(testStopTimeout)
		}
		select {
		case <-runErr:
		case <-time.After(testReturnTimeout):
		}
	}()

	// The TTL is five seconds. With no user request, the process must still be
	// ready well before that deadline. The buggy implementation compares the
	// current time to Unix epoch because lastUse is still zero and stops on the
	// first ~1s ticker iteration.
	time.Sleep(1500 * time.Millisecond)
	if got := p.State(); got != StateReady {
		t.Fatalf("process state after 1.5s with ttl=5s = %s, want ready", got)
	}
}
