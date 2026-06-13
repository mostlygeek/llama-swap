//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"syscall"
)

// setProcAttributes sets platform-specific process attributes. CREATE_NO_WINDOW
// keeps the upstream from spawning its own console window.
func setProcAttributes(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

// terminateProcessTree requests a graceful shutdown of the whole process tree
// rooted at cmd.Process. Windows has no SIGTERM or process-group signalling, so
// we shell out to `taskkill /t`, which walks the child tree by PID — the
// equivalent of signalling a Unix process group. Without /f, taskkill asks the
// processes to close rather than force-killing them.
func terminateProcessTree(cmd *exec.Cmd) error {
	return taskkillProcessTree(cmd, false)
}

// killProcessTree force-terminates the whole process tree rooted at cmd.Process
// via `taskkill /f /t`, so any descendant that ignored or outlived the graceful
// request is killed alongside the parent rather than leaked as an orphan.
func killProcessTree(cmd *exec.Cmd) error {
	return taskkillProcessTree(cmd, true)
}

// taskkillProcessTree runs taskkill against cmd.Process.Pid. The /t flag
// terminates the process together with any child processes it started, which is
// the Windows analogue of signalling a Unix process group via its negative PID.
// When force is true the /f flag force-kills; otherwise taskkill requests a
// graceful close.
func taskkillProcessTree(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	args := make([]string, 0, 4)
	if force {
		args = append(args, "/f")
	}
	args = append(args, "/t", "/pid", fmt.Sprintf("%d", cmd.Process.Pid))
	kill := exec.Command("taskkill", args...)
	setProcAttributes(kill)
	return kill.Run()
}
