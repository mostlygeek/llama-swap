//go:build windows

package main

import "os"

// stopProcess terminates the given PID. Windows has no SIGTERM, so the serve
// proxy is killed directly. This wrapper targets Linux/systemd deployments;
// the Windows build exists only to keep cross-compilation and gosec happy.
func stopProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
