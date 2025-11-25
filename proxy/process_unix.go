//go:build !windows

package proxy

import (
	"os/exec"
)

// setProcAttributes sets platform-specific process attributes
func setProcAttributes(cmd *exec.Cmd) {
	// No-op on Unix systems
}
