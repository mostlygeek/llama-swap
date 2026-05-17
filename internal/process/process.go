package process

import (
	"context"
	"time"
)

type ProcessState string

const (
	StateStopped  ProcessState = ProcessState("stopped")
	StateStarting ProcessState = ProcessState("starting")
	StateReady    ProcessState = ProcessState("ready")
	StateCooldown ProcessState = ProcessState("cooldown")
	StateStopping ProcessState = ProcessState("stopping")

	// process is shutdown and will not be restarted
	StateShutdown ProcessState = ProcessState("shutdown")
)

type Process interface {

	// Run starts the process blocks until the process is terminated
	Run(timeout time.Duration) error

	// WaitReady blocks until the process is ready to serve requests
	// or the context is cancelled. It returns nil when the process is ready
	WaitReady(context.Context) error

	Stop(cooldown time.Duration, timeout time.Duration) error
	State() ProcessState
}
