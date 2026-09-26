//go:build darwin

package hw

import (
	"context"

	"github.com/shirou/gopsutil/v4/cpu"
	"golang.org/x/sys/unix"
)

// cpuInfo reads the CPU fields populateCPU needs directly from sysctl instead
// of calling cpu.InfoWithContext. On Apple Silicon gopsutil also probes the
// CPU frequency through IOKit and passes an unchecked NULL CFDataRef to
// CFDataGetLength when the pmgr "voltage-states5-sram" property is missing,
// which crashes the process with SIGSEGV (seen on macOS 27 / M6). The crash
// happens in C code so it cannot be recovered. See issue #1178.
func cpuInfo(context.Context) ([]cpu.InfoStat, error) {
	var info cpu.InfoStat
	info.ModelName, _ = unix.Sysctl("machdep.cpu.brand_string")
	info.VendorID, _ = unix.Sysctl("machdep.cpu.vendor")
	return []cpu.InfoStat{info}, nil
}
