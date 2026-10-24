package perf

import (
	"github.com/mostlygeek/llama-swap/internal/hw"
)

// allProbes lists every probe for an OS in priority order. auto falls back
// to it on mixed or unknown hardware, and when every probe picked for the
// platform fails.
func allProbes(goos string) []string {
	switch goos {
	case "linux":
		return []string{"nvidia-smi", "rocm-smi", "lact", "xpu-smi", "sysfs"}
	case "windows":
		return []string{"nvidia-smi", "d3dkmt"}
	case "darwin":
		return []string{"mactop", "ioreg"}
	}
	return []string{"nvidia-smi", "rocm-smi", "xpu-smi"}
}

// AllProbes lists every probe for an OS in priority order.
func AllProbes(goos string) []string { return allProbes(goos) }

// autoProbes picks probes for the detected hardware:
//
//	Apple Silicon                     mactop, ioreg
//	NVIDIA only (discrete or unified) nvidia-smi
//	AMD APU                           sysfs
//	AMD discrete only                 rocm-smi, lact, sysfs
//	Intel discrete only               xpu-smi, sysfs
//	Windows, non-NVIDIA               d3dkmt
//	mixed or unknown                  allProbes
func autoProbes(goos string, snapshot *hw.HardwareSnapshot) []string {
	if goos == "darwin" {
		return allProbes(goos)
	}
	var gpus []hw.Accelerator
	if snapshot != nil {
		for _, a := range snapshot.Accelerators {
			if a.Kind == "gpu" {
				gpus = append(gpus, a)
			}
		}
	}
	if len(gpus) == 0 {
		return allProbes(goos)
	}

	vendor := vendorOf(gpus[0])
	unified := false
	for _, g := range gpus {
		if vendorOf(g) != vendor {
			return allProbes(goos)
		}
		switch g.Memory.Kind {
		case "unified", "shared_system":
			unified = true
		}
	}

	if goos == "windows" {
		if vendor == "NVIDIA" {
			return []string{"nvidia-smi"}
		}
		return []string{"d3dkmt"}
	}

	switch vendor {
	case "NVIDIA":
		return []string{"nvidia-smi"}
	case "AMD":
		if unified {
			return []string{"sysfs"}
		}
		return []string{"rocm-smi", "lact", "sysfs"}
	case "Intel":
		if !unified {
			return []string{"xpu-smi", "sysfs"}
		}
	}
	return allProbes(goos)
}

func vendorOf(a hw.Accelerator) string {
	if a.Vendor == nil {
		return ""
	}
	return *a.Vendor
}
