package perf

import "github.com/shirou/gopsutil/v4/mem"

// ReadHostMemAvailableMB returns the host's currently available RAM in MiB
// (MemAvailable on Linux). It is a fresh, synchronous read. ok is false when
// the platform reading fails. On UMA systems (APUs/iGPUs) GPU allocations and
// host memory share one physical pool, so budget gates need both numbers.
func ReadHostMemAvailableMB() (availMB int, ok bool) {
	vm, err := mem.VirtualMemory()
	if err != nil || vm == nil {
		return 0, false
	}
	return int(vm.Available / (1024 * 1024)), true
}
