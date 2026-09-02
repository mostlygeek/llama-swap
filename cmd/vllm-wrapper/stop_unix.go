//go:build !windows

package main

import "syscall"

// stopProcess sends SIGTERM to the given PID so the serve proxy can shut down
// gracefully (putting vLLM to sleep) before exiting.
func stopProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}
