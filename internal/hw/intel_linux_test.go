//go:build linux

package hw

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const xpusmiListing = `{
  "device_list": [
    {
      "device_function_type": "physical",
      "device_id": 0,
      "device_name": "Intel(R) Data Center GPU Flex 170",
      "device_state": "Normal",
      "device_type": "Discrete GPU",
      "drm_device": "/dev/dri/card1",
      "pci_bdf_address": "0000:4d:00.0",
      "pci_device_id": "0x56c0",
      "uuid": "00000000-0000-0000-6769-df256e271362",
      "vendor_name": "Intel(R) Corporation"
    }
  ]
}`

const xpusmiDeviceDetail = `{
  "device_id": 0,
  "device_name": "Intel(R) Data Center GPU Flex 170",
  "device_type": "Discrete GPU",
  "driver_version": "XE_1.0.4_23.4.15",
  "pci_bdf_address": "0000:4d:00.0",
  "pci_device_id": "0x56c0",
  "memory_physical_size": "14248.00 MiB",
  "memory_physical_size_byte": 14942253056
}`

const xpusmiB70Detail = `{
  "device_id": 0,
  "device_name": "Intel(R) Arc(TM) Pro B70 Graphics",
  "device_type": "Discrete GPU",
  "driver_version": "17012946",
  "pci_bdf_address": "0000:03:00.0",
  "pci_device_id": "0xE223",
  "memory_physical_size": "32696.00 MiB",
  "memory_physical_size_byte": 34282209280
}`

func TestHardware_IntelXPUSMIDeviceParsing(t *testing.T) {
	var device xpusmiDevice
	if err := json.Unmarshal([]byte(xpusmiDeviceDetail), &device); err != nil {
		t.Fatal(err)
	}
	got := xpusmiRecordToAccelerator(device, "0000:4d:00.0")

	if got.identity != "0000:4d:00.0" {
		t.Errorf("identity = %q, want 0000:4d:00.0", got.identity)
	}
	if stringValue(got.value.Vendor) != "Intel" {
		t.Errorf("vendor = %q, want Intel", stringValue(got.value.Vendor))
	}
	if stringValue(got.value.Model) != "Intel(R) Data Center GPU Flex 170" {
		t.Errorf("model = %q", stringValue(got.value.Model))
	}
	if stringValue(got.value.Architecture) != "Alchemist" {
		t.Errorf("architecture = %q, want Alchemist (0x56c0 = ATS-M)", stringValue(got.value.Architecture))
	}
	if got.value.Memory.Kind != "dedicated" {
		t.Errorf("memory kind = %q, want dedicated", got.value.Memory.Kind)
	}
	if got.value.Memory.CapacityBytes == nil || *got.value.Memory.CapacityBytes != 14942253056 {
		t.Errorf("memory capacity = %v, want 14942253056", got.value.Memory.CapacityBytes)
	}
	if got.value.Driver == nil || stringValue(got.value.Driver.Version) != "XE_1.0.4_23.4.15" {
		t.Errorf("driver = %+v", got.value.Driver)
	}
	if got.value.Driver == nil || stringValue(got.value.Driver.Name) != "xe" {
		t.Errorf("driver name = %q, want xe inferred from XE_ prefix", stringValue(got.value.Driver.Name))
	}
}

func TestHardware_IntelXPUSMIB70(t *testing.T) {
	// dumbo's card: a bare numeric driver version (no I915_/XE_ brand) must
	// leave the driver name for sysfs to fill, and the Battlemage G31
	// device ID (0xE223) must resolve architecture and model.
	var device xpusmiDevice
	if err := json.Unmarshal([]byte(xpusmiB70Detail), &device); err != nil {
		t.Fatal(err)
	}
	got := xpusmiRecordToAccelerator(device, "0000:03:00.0")

	if stringValue(got.value.Architecture) != "Battlemage" {
		t.Errorf("architecture = %q, want Battlemage", stringValue(got.value.Architecture))
	}
	if got.value.Driver == nil || got.value.Driver.Name != nil {
		t.Errorf("driver = %+v, want nil name with bare numeric version", got.value.Driver)
	}
	if got.value.Memory.Kind != "dedicated" || got.value.Memory.CapacityBytes == nil || *got.value.Memory.CapacityBytes != 34282209280 {
		t.Errorf("memory = %+v, want dedicated 34282209280 bytes", got.value.Memory)
	}
}

func TestHardware_IntelXPUSMII915DriverName(t *testing.T) {
	detail := `{
  "device_id": 0,
  "device_name": "Intel(R) Data Center GPU Flex 170",
  "device_type": "Discrete GPU",
  "driver_version": "I915_23.4.15_PSB_230307.15",
  "pci_bdf_address": "0000:4d:00.0",
  "pci_device_id": "0x56c0",
  "memory_physical_size_byte": 14942253056
}`
	var device xpusmiDevice
	if err := json.Unmarshal([]byte(detail), &device); err != nil {
		t.Fatal(err)
	}
	got := xpusmiRecordToAccelerator(device, "0000:4d:00.0")
	if got.value.Driver == nil || stringValue(got.value.Driver.Name) != "i915" {
		t.Errorf("driver name = %q, want i915 inferred from I915_ prefix", stringValue(got.value.Driver.Name))
	}
}

func TestHardware_IntelXPUSMIIntegratedKeepsSharedSystem(t *testing.T) {
	detail := `{
  "device_id": 1,
  "device_name": "Intel(R) Graphics",
  "device_type": "Integrated GPU",
  "driver_version": "i915",
  "pci_bdf_address": "0000:00:02.0",
  "memory_physical_size_byte": 8589934592
}`
	var device xpusmiDevice
	if err := json.Unmarshal([]byte(detail), &device); err != nil {
		t.Fatal(err)
	}
	got := xpusmiRecordToAccelerator(device, "0000:00:02.0")
	if got.value.Memory.Kind != "shared_system" {
		t.Errorf("memory kind = %q, want shared_system", got.value.Memory.Kind)
	}
}

func TestHardware_IntelXPUSMIMissingMemoryStaysDedicatedWithoutCapacity(t *testing.T) {
	// Older xpu-smi builds omit memory fields; kind stays dedicated for a
	// discrete card but capacity stays unset rather than inventing a value.
	detail := `{
  "device_id": 0,
  "device_name": "Intel(R) Arc(TM) A770 Graphics",
  "device_type": "Discrete GPU",
  "pci_bdf_address": "0000:03:00.0"
}`
	var device xpusmiDevice
	if err := json.Unmarshal([]byte(detail), &device); err != nil {
		t.Fatal(err)
	}
	got := xpusmiRecordToAccelerator(device, "0000:03:00.0")
	if got.value.Memory.Kind != "dedicated" || got.value.Memory.CapacityBytes != nil {
		t.Errorf("memory = %+v, want dedicated with nil capacity", got.value.Memory)
	}
}

func TestHardware_IntelXPUSMIDiscoveryFallsBackToDetailBDF(t *testing.T) {
	// The listing's BDF is used when the detail query fails or omits it;
	// xpusmiRecordToAccelerator receives the identity from detectXPUSMI, so
	// verify normalizePCIIdentity handles the 0000:4d:00.0 shape used by
	// xpu-smi (already domain-qualified).
	if got := normalizePCIIdentity("0000:4d:00.0"); got != "0000:4d:00.0" {
		t.Errorf("normalizePCIIdentity() = %q, want unchanged", got)
	}
}

func TestHardware_IntelXPUSMIAbsent(t *testing.T) {
	// On machines without xpu-smi (or without PATH access), detection must
	// silently contribute nothing.
	if got := detectIntel(context.Background()); got != nil {
		t.Errorf("detectIntel() = %+v, want nil without xpu-smi", got)
	}
}

func TestHardware_IntelXPUSMIMergesOverSysfsSharedSystem(t *testing.T) {
	// The end-to-end fix: a discrete Arc card that sysfs reported as
	// shared_system (no mem_info_vram_total) must end up dedicated with the
	// xpu-smi capacity after finalizeAccelerators merges the two records.
	// This fixture has no pci_device_id, so the xpu-smi record carries no
	// architecture — proving the sysfs record still contributes it.
	detail := `{
  "device_id": 0,
  "device_name": "Intel(R) Arc(TM) Pro B70 Graphics",
  "device_type": "Discrete GPU",
  "driver_version": "17012946",
  "pci_bdf_address": "0000:03:00.0",
  "memory_physical_size_byte": 34282209280
}`
	var device xpusmiDevice
	if err := json.Unmarshal([]byte(detail), &device); err != nil {
		t.Fatal(err)
	}
	xpusmi := xpusmiRecordToAccelerator(device, "0000:03:00.0")
	sysfs := detectedAccelerator{
		identity: "0000:03:00.0",
		value: Accelerator{
			Kind:         "gpu",
			Vendor:       stringPtr("Intel"),
			Architecture: stringPtr("Battlemage"),
			Driver:       &Driver{Name: stringPtr("xe"), Version: stringPtr("17012946")},
			Memory:       AcceleratorMemory{Kind: "shared_system"},
		},
	}

	got := finalizeAccelerators([]detectedAccelerator{xpusmi, sysfs})
	if len(got) != 1 {
		t.Fatalf("finalizeAccelerators() returned %d devices, want 1: %+v", len(got), got)
	}
	accel := got[0]
	if accel.Memory.Kind != "dedicated" {
		t.Errorf("merged memory kind = %q, want dedicated", accel.Memory.Kind)
	}
	if accel.Memory.CapacityBytes == nil || *accel.Memory.CapacityBytes != 34282209280 {
		t.Errorf("merged memory capacity = %v, want 34282209280", accel.Memory.CapacityBytes)
	}
	if stringValue(accel.Architecture) != "Battlemage" {
		t.Errorf("merged architecture = %q, want Battlemage (sysfs contribution preserved)", stringValue(accel.Architecture))
	}
	if accel.Driver == nil || stringValue(accel.Driver.Name) != "xe" {
		t.Errorf("merged driver = %+v, want sysfs-provided name xe", accel.Driver)
	}
}

func TestHardware_IntelSysfsDiscreteWithoutXPUSMI(t *testing.T) {
	// Regression guard for the pure-sysfs path: with mem_info_vram_total
	// present the card already reports dedicated; without xpu-smi nothing
	// changes for it. sysfsRoot and driRoot are deliberately separate
	// trees (as /sys and /dev/dri are on a real host) so a probe that looks
	// for render nodes under the sysfs root fails here.
	sysfsRoot := t.TempDir()
	driRoot := t.TempDir()
	sysfsDRM := filepath.Join(sysfsRoot, "class", "drm")
	devicePath := filepath.Join(sysfsRoot, "pci", "0000:4d:00.0")
	for _, path := range []string{
		filepath.Join(sysfsDRM, "card1"),
		filepath.Join(devicePath, "drm", "renderD128"),
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{
		"vendor":              "0x8086\n",
		"device":              "0x56c0\n",
		"mem_info_vram_total": "14942253056\n",
	} {
		if err := os.WriteFile(filepath.Join(devicePath, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(devicePath, filepath.Join(sysfsDRM, "card1", "device")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(driRoot, "renderD128"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := detectDRMSysfsFrom(sysfsRoot, driRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("detectDRMSysfsFrom() = %+v, want 1 device", got)
	}
	if got[0].value.Memory.Kind != "dedicated" || got[0].value.Memory.CapacityBytes == nil {
		t.Errorf("sysfs memory = %+v, want dedicated with capacity", got[0].value.Memory)
	}
	if stringValue(got[0].value.Architecture) != "Alchemist" {
		t.Errorf("sysfs architecture = %q, want Alchemist (0x56c0 = ATS-M)", stringValue(got[0].value.Architecture))
	}
}
