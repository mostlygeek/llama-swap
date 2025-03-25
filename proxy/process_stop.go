//go:build !windows

package proxy

import "syscall"

func (p *Process) terminateProcess() error {
	return p.cmd.Process.Signal(syscall.SIGTERM)
}
