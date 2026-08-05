//go:build linux

package hw

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/host"
)

var drmCardPattern = regexp.MustCompile(`^card\d+$`)

func detectPlatform(ctx context.Context, snapshot *HardwareSnapshot) ([]detectedAccelerator, error) {
	var result []detectedAccelerator
	if nvidia, err := detectNvidia(ctx); err == nil {
		result = append(result, nvidia...)
	}
	result = append(result, detectAMD(ctx)...)
	if sysfs, err := detectDRMSysfs(); err == nil {
		result = append(result, sysfs...)
	}
	return result, nil
}

func detectEnvironment(ctx context.Context, info *host.InfoStat) ExecutionEnvironment {
	kernel := strings.ToLower(info.KernelVersion)
	if strings.Contains(kernel, "microsoft") || strings.Contains(kernel, "wsl") {
		return ExecutionEnvironment{Kind: "wsl", Name: stringPtr("WSL")}
	}

	system := strings.ToLower(strings.TrimSpace(info.VirtualizationSystem))
	role := strings.ToLower(strings.TrimSpace(info.VirtualizationRole))
	if role == "guest" {
		switch system {
		case "docker", "lxc", "rkt", "podman", "containerd", "systemd-nspawn":
			return ExecutionEnvironment{Kind: "container", Name: nonEmptyStringPtr(displayEnvironmentName(system))}
		default:
			return ExecutionEnvironment{Kind: "virtual_machine", Name: nonEmptyStringPtr(displayEnvironmentName(system))}
		}
	}
	if system != "" && role == "host" {
		return ExecutionEnvironment{Kind: "native"}
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return ExecutionEnvironment{Kind: "container", Name: stringPtr("Docker")}
	}
	if path, err := exec.LookPath("systemd-detect-virt"); err == nil {
		command := exec.CommandContext(ctx, path)
		output, commandErr := command.Output()
		name := strings.TrimSpace(string(output))
		if commandErr == nil && name != "" && name != "none" {
			kind := "virtual_machine"
			if isContainerEnvironment(name) {
				kind = "container"
			}
			return ExecutionEnvironment{Kind: kind, Name: nonEmptyStringPtr(displayEnvironmentName(name))}
		}
		if exitErr := new(exec.ExitError); errors.As(commandErr, &exitErr) && exitErr.ExitCode() == 1 {
			return ExecutionEnvironment{Kind: "native"}
		}
	}
	return ExecutionEnvironment{Kind: "unknown"}
}

func isContainerEnvironment(value string) bool {
	switch strings.ToLower(value) {
	case "docker", "lxc", "rkt", "podman", "containerd", "systemd-nspawn":
		return true
	default:
		return false
	}
}

func displayEnvironmentName(value string) string {
	switch strings.ToLower(value) {
	case "docker":
		return "Docker"
	case "kvm":
		return "KVM"
	case "vmware":
		return "VMware"
	case "vbox":
		return "VirtualBox"
	case "hyperv":
		return "Hyper-V"
	case "lxc":
		return "LXC"
	default:
		return value
	}
}

func platformMemoryCapacity(total uint64) uint64 {
	if physical := zoneinfoMemoryCapacity("/proc/zoneinfo", uint64(os.Getpagesize())); physical > total {
		total = physical
	}
	return limitedMemoryCapacity(total, []string{
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
	})
}

func zoneinfoMemoryCapacity(path string, pageSize uint64) uint64 {
	if pageSize == 0 {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	var presentPages uint64
	includeZone := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "Node" && fields[2] == "zone" {
			includeZone = fields[3] != "Device"
			continue
		}
		if !includeZone || len(fields) != 2 || fields[0] != "present" {
			continue
		}
		pages, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || pages > ^uint64(0)-presentPages {
			return 0
		}
		presentPages += pages
	}
	if presentPages == 0 || presentPages > ^uint64(0)/pageSize {
		return 0
	}
	return presentPages * pageSize
}

func limitedMemoryCapacity(total uint64, paths []string) uint64 {
	capacity := total
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(data))
		if value == "" || value == "max" {
			continue
		}
		limit, err := strconv.ParseUint(value, 10, 64)
		if err == nil && limit > 0 && limit < capacity {
			capacity = limit
		}
	}
	return capacity
}

func detectDRMSysfs() ([]detectedAccelerator, error) {
	paths, err := filepath.Glob("/sys/class/drm/card*")
	if err != nil {
		return nil, err
	}
	var result []detectedAccelerator
	for _, cardPath := range paths {
		if !drmCardPattern.MatchString(filepath.Base(cardPath)) {
			continue
		}
		devicePath := filepath.Join(cardPath, "device")
		resolved, err := filepath.EvalSymlinks(devicePath)
		if err != nil || !hasAccessibleRenderNode(devicePath) {
			continue
		}
		vendorID := readTrimmed(filepath.Join(devicePath, "vendor"))
		vendor := pciVendorName(vendorID)
		if vendor == "" {
			continue
		}
		memoryBytes, _ := strconv.ParseUint(readTrimmed(filepath.Join(devicePath, "mem_info_vram_total")), 10, 64)
		memory := AcceleratorMemory{Kind: "shared_system"}
		if memoryBytes > 0 {
			memory.Kind = "dedicated"
			memory.CapacityBytes = uint64Ptr(memoryBytes)
		}
		driverName := ""
		if driverPath, err := filepath.EvalSymlinks(filepath.Join(devicePath, "driver")); err == nil {
			driverName = filepath.Base(driverPath)
		}
		driverVersion := ""
		if driverName != "" {
			driverVersion = readTrimmed(filepath.Join("/sys/module", driverName, "version"))
		}
		var driver *Driver
		if driverName != "" || driverVersion != "" {
			driver = &Driver{Name: nonEmptyStringPtr(driverName), Version: nonEmptyStringPtr(driverVersion)}
		}
		model := readTrimmed(filepath.Join(devicePath, "product_name"))
		powerLimit := readPowerLimit(devicePath)
		result = append(result, detectedAccelerator{
			identity: normalizePCIIdentity(filepath.Base(resolved)),
			value: Accelerator{
				Kind:            "gpu",
				Vendor:          stringPtr(vendor),
				Model:           nonEmptyStringPtr(model),
				Memory:          memory,
				Driver:          driver,
				PowerLimitWatts: powerLimit,
			},
		})
	}
	return result, nil
}

func hasAccessibleRenderNode(devicePath string) bool {
	nodes, _ := filepath.Glob(filepath.Join(devicePath, "drm", "renderD*"))
	for _, node := range nodes {
		if _, err := os.Stat(filepath.Join("/dev/dri", filepath.Base(node))); err == nil {
			return true
		}
	}
	return false
}

func pciVendorName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0x10de":
		return "NVIDIA"
	case "0x1002", "0x1022":
		return "AMD"
	case "0x8086":
		return "Intel"
	case "0x106b":
		return "Apple"
	default:
		return ""
	}
}

func readPowerLimit(devicePath string) *float64 {
	paths, _ := filepath.Glob(filepath.Join(devicePath, "hwmon", "hwmon*", "power1_cap"))
	for _, path := range paths {
		microwatts, err := strconv.ParseFloat(readTrimmed(path), 64)
		if err == nil && microwatts > 0 {
			return float64Ptr(microwatts / 1_000_000)
		}
	}
	return nil
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
