//go:build windows

package hw

import "testing"

func TestHardware_WindowsVendorNormalization(t *testing.T) {
	if got := windowsVendor("", "AMD Radeon RX 7900 XTX", `PCI\VEN_1002&DEV_744C`); got != "AMD" {
		t.Fatalf("windowsVendor() = %q", got)
	}
	if got := windowsVendor("NVIDIA", "GeForce RTX 4090", `PCI\VEN_10DE&DEV_2684`); got != "NVIDIA" {
		t.Fatalf("windowsVendor() = %q", got)
	}
}

func TestHardware_WindowsFiltersSoftwareAdapters(t *testing.T) {
	for _, name := range []string{"Microsoft Basic Display Adapter", "Microsoft Remote Display Adapter"} {
		if !isSoftwareDisplayAdapter(name) {
			t.Fatalf("isSoftwareDisplayAdapter(%q) = false", name)
		}
	}
}
