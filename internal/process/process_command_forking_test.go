//go:build !windows

package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// TestProcessCommand_StopForkingWrapper is a regression for the bug reported
// against v219 where Stop would hang indefinitely when the upstream command
// is a shell wrapper that forks the real binary (e.g. `#!/bin/bash` then
// `"$@"`). After SIGTERM the wrapper dies but the grandchild inherits the
// stdout/stderr pipes; cmd.Wait() blocks waiting for the pipe-copy goroutine
// to drain EOF, which never happens while the grandchild holds the fds.
//
// The fix is cmd.WaitDelay (combined with exec.CommandContext + cmd.Cancel),
// which causes the runtime to force-close the pipes after the delay so
// cmd.Wait() — and therefore Stop — returns.
func TestProcessCommand_StopForkingWrapper(t *testing.T) {
	skipIfNoSimpleResponder(t)

	port := getFreePort(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	// Wrapper script: backgrounds the child (which inherits stdout/stderr),
	// records its PID for cleanup, then waits. When SIGTERM hits bash it
	// dies without forwarding the signal; the grandchild keeps running and
	// keeps the inherited pipe fds open. This is the scenario reported in
	// the v219 regression.
	wrapper := filepath.Join(dir, "wrapper.sh")
	script := fmt.Sprintf("#!/bin/bash\n%q -port %d -silent &\necho $! > %q\nwait\n",
		simpleResponderPath, port, pidFile)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Cleanup(func() { killChildFromPidFile(pidFile) })

	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                wrapper,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})
	// Shrink the pipe-close backstop so the test doesn't sit at the
	// production default (10s). Must be set before Run() so doStart picks
	// it up when building the cmd.
	const testWaitDelay = 250 * time.Millisecond
	p.waitDelay = testWaitDelay

	runErr := runAsync(t, p)

	// Stop must return within a bounded time even though the grandchild
	// is still holding the pipe open. Budget is generous on top of
	// testWaitDelay to absorb scheduling jitter on slow CI runners; the
	// pre-fix behaviour was an unbounded hang, so any reasonable cap
	// distinguishes pass from fail.
	stopReturned := make(chan error, 1)
	stopStart := time.Now()
	go func() { stopReturned <- p.Stop(testStopTimeout) }()

	const stopBudget = testWaitDelay + 2*time.Second
	select {
	case err := <-stopReturned:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
		t.Logf("Stop returned in %v", time.Since(stopStart))
	case <-time.After(stopBudget):
		t.Fatalf("Stop did not return within %v — cmd.Wait() likely hung on inherited pipe", stopBudget)
	}

	if got := p.State(); got != StateStopped {
		t.Errorf("after Stop: expected state %s, got %s", StateStopped, got)
	}

	select {
	case <-runErr:
	case <-time.After(testReturnTimeout):
		t.Errorf("Run did not return after Stop")
	}
}

// TestProcessCommand_StopHonorsGracefulTimeout is a regression for the bug
// where cmd.WaitDelay capped the graceful shutdown window. killProcess used to
// cancel the cmd context to deliver SIGTERM, which starts cmd.WaitDelay
// immediately; a process whose SIGTERM handler needs longer than WaitDelay to
// finish was force-killed early even though Stop was given a much longer
// timeout. The fix sends the signal directly so WaitDelay measures from process
// exit (its inherited-pipe backstop role), leaving the graceful window to the
// caller's Stop timeout.
func TestProcessCommand_StopHonorsGracefulTimeout(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "graceful.done")
	ready := filepath.Join(dir, "trap.ready")

	// On SIGTERM, sleep past the (short) WaitDelay, then write the marker and
	// exit cleanly. If WaitDelay still drove the kill, bash would be SIGKILLed
	// mid-handler and the marker would never be written. The ready file is
	// written only after the trap is installed so the test does not race
	// SIGTERM ahead of it (CheckEndpoint:none marks ready before bash runs).
	script := filepath.Join(dir, "graceful.sh")
	body := fmt.Sprintf(
		"#!/bin/bash\ncleanup() { sleep 0.6; echo done > %q; exit 0; }\ntrap cleanup SIGTERM\necho ready > %q\nwhile true; do sleep 0.1; done\n",
		marker, ready,
	)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := newProcessCommand(t, config.ModelConfig{
		Cmd:           script,
		Proxy:         "http://127.0.0.1:1", // unused: health check disabled
		CheckEndpoint: "none",
	})
	// WaitDelay shorter than the handler's 0.6s sleep, and far shorter than the
	// Stop timeout below — this is the window the old code mis-killed in.
	p.waitDelay = 200 * time.Millisecond

	runErr := runAsync(t, p)

	// Wait until the trap is installed before stopping.
	trapDeadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(trapDeadline) {
			t.Fatalf("script did not install SIGTERM trap in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	stopStart := time.Now()
	if err := p.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	elapsed := time.Since(stopStart)

	// The handler must have run to completion (marker written) rather than
	// being force-killed at waitDelay.
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("graceful handler did not complete (marker missing): %v", err)
	}
	// And Stop must have waited for the handler (>~0.6s), not returned at the
	// 200ms waitDelay.
	if elapsed < 500*time.Millisecond {
		t.Fatalf("Stop returned in %v — process was killed before its graceful handler finished", elapsed)
	}

	if got := p.State(); got != StateStopped {
		t.Errorf("after Stop: expected state %s, got %s", StateStopped, got)
	}
	select {
	case <-runErr:
	case <-time.After(testReturnTimeout):
		t.Errorf("Run did not return after Stop")
	}
}

// TestProcessCommand_StopReapsForkedGrandchild verifies that stopping a forking
// wrapper takes down the backgrounded grandchild too, rather than leaving it as
// an orphan. The fix is Setpgid (runtime_unix.go): the wrapper leads its own
// process group, so the stop signal is delivered to the whole group via the
// negative PID and reaches the grandchild the wrapper never reaped.
func TestProcessCommand_StopReapsForkedGrandchild(t *testing.T) {
	skipIfNoSimpleResponder(t)

	port := getFreePort(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	wrapper := filepath.Join(dir, "wrapper.sh")
	script := fmt.Sprintf("#!/bin/bash\n%q -port %d -silent &\necho $! > %q\nwait\n",
		simpleResponderPath, port, pidFile)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Cleanup(func() { killChildFromPidFile(pidFile) })

	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                wrapper,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})

	runErr := runAsync(t, p)

	// Read the grandchild PID the wrapper recorded.
	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && pid > 0 {
				childPID = pid
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("wrapper did not record grandchild PID")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := p.Stop(testStopTimeout); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop the grandchild must be gone. Signal 0 probes liveness without
	// actually sending a signal; give it a brief window to exit after the
	// group SIGTERM.
	proc, err := os.FindProcess(childPID)
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	gone := false
	for i := 0; i < 100; i++ {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			gone = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !gone {
		t.Errorf("grandchild PID %d still alive after Stop — process group was not reaped", childPID)
	}

	select {
	case <-runErr:
	case <-time.After(testReturnTimeout):
		t.Errorf("Run did not return after Stop")
	}
}

// killChildFromPidFile reads a PID written by the wrapper script and SIGKILLs
// it so leaked orphans don't accumulate between test runs. Best-effort.
func killChildFromPidFile(pidFile string) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Kill()
}
