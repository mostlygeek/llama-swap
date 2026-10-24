package probe

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sysfsRoot is the sysfs mount point. Tests point it at a fixture tree.
var sysfsRoot = "/sys"

// PCI vendor IDs as sysfs prints them.
const (
	vendorNVIDIA = "0x10de"
	vendorAMD    = "0x1002"
	vendorIntel  = "0x8086"
)

// pciGPU is a display-class PCI device found in sysfs.
type pciGPU struct {
	Key       string
	Vendor    string
	Suspended bool
}

// listPCIGPUs returns display-class PCI devices from the given vendor, or
// from every vendor when vendor is "", sorted by PCI address. Reading these
// attributes never wakes a device.
func listPCIGPUs(vendor string) []pciGPU {
	devices, err := os.ReadDir(filepath.Join(sysfsRoot, "bus", "pci", "devices"))
	if err != nil {
		return nil
	}
	var gpus []pciGPU
	for _, d := range devices {
		dir := filepath.Join(sysfsRoot, "bus", "pci", "devices", d.Name())
		if !strings.HasPrefix(readString(filepath.Join(dir, "class")), "0x03") {
			continue
		}
		v := strings.ToLower(readString(filepath.Join(dir, "vendor")))
		if vendor != "" && v != vendor {
			continue
		}
		key := NormalizePCI(d.Name())
		if key == "" {
			continue
		}
		gpus = append(gpus, pciGPU{Key: key, Vendor: v, Suspended: runtimeSuspended(dir)})
	}
	sort.Slice(gpus, func(i, j int) bool { return gpus[i].Key < gpus[j].Key })
	return gpus
}

// runtimeSuspended reports whether the kernel has runtime-suspended the
// device at dir. The attribute is kernel state, so reading it never wakes
// the device.
func runtimeSuspended(dir string) bool {
	return readString(filepath.Join(dir, "power", "runtime_status")) == "suspended"
}

// checkSuspended splits a vendor's GPUs into suspended samples and awake PCI
// keys. With skip false nothing is reported as suspended.
func checkSuspended(vendor, name string, skip bool) (suspended []GpuSample, awake []string) {
	if !skip {
		return nil, nil
	}
	for _, g := range listPCIGPUs(vendor) {
		if g.Suspended {
			suspended = append(suspended, GpuSample{Key: g.Key, Name: name, State: GpuStateSuspended})
		} else {
			awake = append(awake, g.Key)
		}
	}
	return suspended, awake
}

func readString(path string) string {
	b, err := os.ReadFile(path) // #nosec G304 -- paths built from sysfsRoot
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
