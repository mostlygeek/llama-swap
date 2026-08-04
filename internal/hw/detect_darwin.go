//go:build darwin

package hw

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/host"
)

var appleArchitecturePattern = regexp.MustCompile(`(?i)\bApple\s+M\d+`)

func detectPlatform(ctx context.Context, snapshot *HardwareSnapshot) ([]detectedAccelerator, error) {
	output, err := exec.CommandContext(ctx, "system_profiler", "-json", "SPHardwareDataType", "SPDisplaysDataType").Output()
	if err != nil {
		return nil, fmt.Errorf("running system_profiler: %w", err)
	}
	return parseSystemProfiler(output, snapshot)
}

func detectEnvironment(ctx context.Context, _ *host.InfoStat) ExecutionEnvironment {
	output, err := exec.CommandContext(ctx, "sysctl", "-n", "kern.hv_vmm_present").Output()
	if err != nil {
		return ExecutionEnvironment{Kind: "unknown"}
	}
	switch strings.TrimSpace(string(output)) {
	case "1":
		return ExecutionEnvironment{Kind: "virtual_machine"}
	case "0":
		return ExecutionEnvironment{Kind: "native"}
	default:
		return ExecutionEnvironment{Kind: "unknown"}
	}
}

func platformMemoryCapacity(total uint64) uint64 { return total }

func parseSystemProfiler(data []byte, snapshot *HardwareSnapshot) ([]detectedAccelerator, error) {
	var root map[string][]map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing system_profiler output: %w", err)
	}
	if hardware := root["SPHardwareDataType"]; len(hardware) > 0 {
		chip := firstMapString(hardware[0], "chip_type", "cpu_type")
		if snapshot.CPU.Model == nil {
			snapshot.CPU.Model = nonEmptyStringPtr(chip)
		}
		if strings.HasPrefix(strings.ToLower(chip), "apple ") {
			snapshot.CPU.Vendor = stringPtr("Apple")
		}
	}

	var result []detectedAccelerator
	for index, display := range root["SPDisplaysDataType"] {
		model := firstMapString(display, "sppci_model", "chipset_model", "_name")
		vendorRaw := firstMapString(display, "spdisplays_vendor", "vendor")
		vendor := displayVendor(vendorRaw, model)
		if model == "" && vendor == "" {
			continue
		}

		memory := AcceleratorMemory{Kind: "unknown"}
		if vendor == "Apple" {
			memory.Kind = "unified"
			memory.CapacityBytes = uint64Ptr(snapshot.Memory.CapacityBytes)
		} else {
			shared := strings.EqualFold(firstMapString(display, "spdisplays_vram_shared"), "spdisplays_yes")
			if shared {
				memory.Kind = "shared_system"
			} else {
				memory.Kind = "dedicated"
			}
			if capacity := parseProfilerMemory(firstMapString(display, "spdisplays_vram", "_spdisplays_vram")); capacity > 0 {
				memory.CapacityBytes = uint64Ptr(capacity)
			}
		}

		architecture := ""
		if match := appleArchitecturePattern.FindString(model); match != "" {
			architecture = match
		}
		var driver *Driver
		if firstMapString(display, "spdisplays_metal", "spdisplays_metal_support") != "" {
			driver = &Driver{Name: stringPtr("Metal")}
		}
		result = append(result, detectedAccelerator{
			identity: fmt.Sprintf("display-%04d", index),
			value: Accelerator{
				Kind:         "gpu",
				Vendor:       nonEmptyStringPtr(vendor),
				Model:        nonEmptyStringPtr(model),
				Architecture: nonEmptyStringPtr(architecture),
				Memory:       memory,
				Driver:       driver,
			},
		})
	}
	return result, nil
}

func firstMapString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return strings.TrimSpace(typed)
				}
			case json.Number:
				return typed.String()
			case float64:
				return strconv.FormatFloat(typed, 'f', -1, 64)
			}
		}
	}
	return ""
}

func displayVendor(raw, model string) string {
	combined := strings.ToLower(raw + " " + model)
	switch {
	case strings.Contains(combined, "apple"):
		return "Apple"
	case strings.Contains(combined, "nvidia"):
		return "NVIDIA"
	case strings.Contains(combined, "amd"), strings.Contains(combined, "ati"):
		return "AMD"
	case strings.Contains(combined, "intel"):
		return "Intel"
	default:
		if before, _, ok := strings.Cut(raw, " ("); ok {
			return strings.TrimSpace(before)
		}
		return strings.TrimSpace(raw)
	}
}

func parseProfilerMemory(value string) uint64 {
	fields := strings.Fields(strings.ReplaceAll(value, ",", ""))
	if len(fields) == 0 {
		return 0
	}
	amount, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || amount <= 0 {
		return 0
	}
	multiplier := float64(1024 * 1024)
	if len(fields) > 1 {
		switch strings.ToLower(fields[1]) {
		case "kb":
			multiplier = 1024
		case "gb":
			multiplier = 1024 * 1024 * 1024
		case "tb":
			multiplier = 1024 * 1024 * 1024 * 1024
		}
	}
	return uint64(amount * multiplier)
}
