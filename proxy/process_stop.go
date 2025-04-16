//go:build !windows

package proxy

import (
	"fmt"
	"syscall"
)

func (p *Process) terminateProcess() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return fmt.Errorf("no process to terminate")
	}

	pgid, err := syscall.Getpgid(p.cmd.Process.Pid)
	if err != nil {
		return fmt.Errorf("failed to get pgid: %v", err)
	}

	p.proxyLogger.Infof("Sending SIGTERM to process group -%d", pgid)
	return syscall.Kill(-pgid, syscall.SIGTERM)
}
