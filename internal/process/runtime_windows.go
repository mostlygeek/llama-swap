//go:build windows

package process

import (
	"os/exec"
	"syscall"
)

// setProcAttributes sets platform-specific process attributes
func setProcAttributes(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

// terminateProcessTree asks the upstream process to stop. Windows has no
// process-group signalling here — process-tree teardown is handled by the
// configured CmdStop, which defaults to `taskkill /f /t` — so this preserves
// the previous single-process SIGTERM behaviour.
func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGTERM)
}

// killProcessTree force-terminates the upstream process. Tree teardown on
// Windows relies on CmdStop (taskkill /t); this kills the launched process.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
