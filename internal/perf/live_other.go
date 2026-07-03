//go:build !unix || darwin

package perf

// ReadLiveGpuMemUsedMB is only implemented for the amdgpu sysfs backend on
// non-darwin unix. Elsewhere callers fall back to the monitor's polled sample.
func ReadLiveGpuMemUsedMB() (usedMB int, ok bool) {
	return 0, false
}
