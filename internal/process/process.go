package process

import (
	"context"
	"net/http"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
)

type ProcessState string

const (
	StateStopped  ProcessState = ProcessState("stopped")
	StateStarting ProcessState = ProcessState("starting")
	StateReady    ProcessState = ProcessState("ready")
	StateStopping ProcessState = ProcessState("stopping")

	// process is shutdown and will not be restarted
	StateShutdown ProcessState = ProcessState("shutdown")
)

type Process interface {
	// Run starts the process blocks until the process is terminated.
	// The timeout parameter controls how long to wait for the process to get
	// to a ready state to process traffic
	Run(timeout time.Duration) error

	// EnsureReady starts the process if it is stopped and blocks until it is
	// ready to serve, the start fails, or the context is cancelled. Unlike a
	// caller-side State()+Run()+WaitReady() sequence, the decision to start is
	// made inside the state machine against live state, so it cannot be
	// derailed by a concurrent Stop (e.g. a TTL unload) between snapshot and
	// start. The timeout parameter bounds the health-check wait, as in Run.
	EnsureReady(ctx context.Context, timeout time.Duration) error

	// WaitReady blocks while the process is starting and returns nil once it
	// is ready to serve requests. If no start is in flight (stopped or shut
	// down) it fails fast — wrapping ErrNotStarted — rather than parking,
	// since a waiter parked with no pending start can never be woken.
	WaitReady(context.Context) error

	// Stop blocks until the process has terminated. It returns nil when
	// the process terminated as expected (exit 0)
	Stop(timeout time.Duration) error

	// State returns the current state of the process
	// Note: this is a snapshot of the state at the time of the call
	// and may change at any time after the call returns.
	State() ProcessState

	// ServeHTTP forwards requests to the underlying process
	// Calling it when the process is not ready will result in a
	// 503 response with a body indicating it is a llama-swap-error
	ServeHTTP(http.ResponseWriter, *http.Request)

	// Logger returns the monitor that captures this process's stdout/stderr.
	Logger() *logmon.Monitor
}
