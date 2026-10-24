//go:build !linux

package probe

// PCI vendor IDs as sysfs prints them.
const (
	vendorNVIDIA = "0x10de"
	vendorAMD    = "0x1002"
	vendorIntel  = "0x8086"
)

type pciGPU struct {
	Key       string
	Vendor    string
	Suspended bool
}

// listPCIGPUs has no source outside Linux.
func listPCIGPUs(vendor string) []pciGPU { return nil }

// checkSuspended is a no-op outside Linux: skipSuspended is Linux only.
func checkSuspended(vendor, name string, skip bool) (suspended []GpuSample, awake []string) {
	return nil, nil
}
