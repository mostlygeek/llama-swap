//go:build linux

package hw

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// detectIntel enumerates Intel GPUs with xpu-smi (Intel XPU Manager). The
// sysfs DRM probe reports discrete Arc cards as shared_system memory when the
// driver does not expose mem_info_vram_total, so xpu-smi is the authoritative
// source for dedicated capacity when present. Integrated devices it reports
// keep shared_system, with capacity left unset to avoid double-counting
// system memory.
func detectIntel(ctx context.Context) []detectedAccelerator {
	result, err := detectXPUSMI(ctx)
	if err != nil {
		return nil
	}
	return result
}

type xpusmiDevice struct {
	DeviceID               int         `json:"device_id"`
	DeviceName             string      `json:"device_name"`
	DeviceType             string      `json:"device_type"`
	DriverVersion          string      `json:"driver_version"`
	BDF                    string      `json:"pci_bdf_address"`
	PCIDeviceID            string      `json:"pci_device_id"`
	MemoryPhysicalSizeByte json.Number `json:"memory_physical_size_byte"`
}

func detectXPUSMI(ctx context.Context) ([]detectedAccelerator, error) {
	if _, err := exec.LookPath("xpu-smi"); err != nil {
		return nil, err
	}
	// discovery -d <id> -j prints full per-device detail including
	// memory_physical_size_byte; the bare listing omits memory. Device ids are
	// not known ahead of time, so first list, then query each device.
	output, err := exec.CommandContext(ctx, "xpu-smi", "discovery", "-j").Output()
	if err != nil {
		return nil, fmt.Errorf("querying xpu-smi: %w", err)
	}
	var listing struct {
		DeviceList []struct {
			DeviceID int    `json:"device_id"`
			BDF      string `json:"pci_bdf_address"`
			Name     string `json:"device_name"`
			Type     string `json:"device_type"`
		} `json:"device_list"`
	}
	if err := json.Unmarshal(output, &listing); err != nil {
		return nil, fmt.Errorf("parsing xpu-smi discovery output: %w", err)
	}

	result := make([]detectedAccelerator, 0, len(listing.DeviceList))
	for _, device := range listing.DeviceList {
		detail, err := queryXPUSMIDevice(ctx, device.DeviceID)
		if err != nil {
			continue
		}
		identity := normalizePCIIdentity(detail.BDF)
		if identity == "" {
			identity = normalizePCIIdentity(device.BDF)
		}
		if identity == "" {
			continue
		}
		result = append(result, xpusmiRecordToAccelerator(detail, identity))
	}
	return result, nil
}

func queryXPUSMIDevice(ctx context.Context, deviceID int) (xpusmiDevice, error) {
	var device xpusmiDevice
	// Fixed probe binary, integer device index: no caller-supplied arguments.
	output, err := exec.CommandContext(ctx, "xpu-smi", "discovery", "-d", fmt.Sprint(deviceID), "-j").Output()
	if err != nil {
		return device, fmt.Errorf("querying xpu-smi device %d: %w", deviceID, err)
	}
	if err := json.Unmarshal(output, &device); err != nil {
		return device, fmt.Errorf("parsing xpu-smi device %d detail: %w", deviceID, err)
	}
	if device.BDF == "" {
		return device, fmt.Errorf("xpu-smi device %d missing pci_bdf_address", deviceID)
	}
	return device, nil
}

func xpusmiRecordToAccelerator(device xpusmiDevice, identity string) detectedAccelerator {
	memory := AcceleratorMemory{Kind: "dedicated"}
	if strings.Contains(strings.ToLower(device.DeviceType), "integrated") {
		memory = AcceleratorMemory{Kind: "shared_system"}
	}
	if capacity, err := device.MemoryPhysicalSizeByte.Int64(); err == nil && capacity > 0 {
		memory.CapacityBytes = uint64Ptr(uint64(capacity))
	}
	// The kernel module name is not reported as a field; infer it from the
	// version prefix Intel brands its driver strings with (I915_..., XE_...)
	// and otherwise leave it for the sysfs probe to fill from the driver
	// symlink.
	var driver *Driver
	if version := nonEmptyStringPtr(device.DriverVersion); version != nil {
		var name *string
		switch {
		case strings.HasPrefix(*version, "I915_"):
			name = stringPtr("i915")
		case strings.HasPrefix(*version, "XE_"):
			name = stringPtr("xe")
		}
		driver = &Driver{Name: name, Version: version}
	}
	// Architecture (and a shorter model) from the PCI device-ID table, so
	// it is populated even when the sysfs probe is unavailable.
	var architecture *string
	model := device.DeviceName
	if id, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(device.PCIDeviceID)), "0x"), 16, 16); err == nil {
		if info, ok := intelGPU(uint16(id)); ok {
			architecture = nonEmptyStringPtr(info.Architecture)
			if model == "" {
				model = info.Model
			}
		}
	}
	return detectedAccelerator{
		identity: identity,
		value: Accelerator{
			Kind:         "gpu",
			Vendor:       stringPtr("Intel"),
			Model:        nonEmptyStringPtr(model),
			Architecture: architecture,
			Memory:       memory,
			Driver:       driver,
		},
	}
}
