//go:build windows

package proxy

import (
	"fmt"
	"os/exec"
)

func (p *Process) terminateProcess() error {
	pid := fmt.Sprintf("%d", p.cmd.Process.Pid)
	cmd := exec.Command("taskkill", "/f", "/t", "/pid", pid)
	return cmd.Run()
}
