package hw

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

func Detect(ctx context.Context, detectorVersion string) (HardwareSnapshot, error) {
	detectorVersion = strings.TrimSpace(detectorVersion)
	if detectorVersion == "" {
		return HardwareSnapshot{}, fmt.Errorf("detector version is required")
	}

	snapshot := HardwareSnapshot{
		SchemaVersion: SchemaVersion,
		CapturedAt:    time.Now().UTC(),
		Capture: HardwareCapture{
			Scope:  CaptureScopeInferenceHost,
			Method: CaptureMethodDetected,
			Detector: &DetectorInfo{
				Name:    "llama-swap",
				Version: detectorVersion,
			},
		},
		Architecture: normalizeArchitecture(runtime.GOARCH),
		OperatingSystem: OperatingSystem{
			Family: normalizeOS(runtime.GOOS),
		},
		Environment:  ExecutionEnvironment{Kind: "unknown"},
		Accelerators: []Accelerator{},
	}
	if snapshot.OperatingSystem.Family == "other" {
		snapshot.OperatingSystem.RawFamily = stringPtr(runtime.GOOS)
	}

	if info, err := host.InfoWithContext(ctx); err == nil {
		populateOperatingSystem(&snapshot.OperatingSystem, info)
		snapshot.Environment = detectEnvironment(ctx, info)
	}
	populateCPU(ctx, &snapshot.CPU)

	virtualMemory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return HardwareSnapshot{}, fmt.Errorf("detecting system memory: %w", err)
	}
	capacity := platformMemoryCapacity(virtualMemory.Total)
	if capacity == 0 {
		return HardwareSnapshot{}, fmt.Errorf("detecting system memory: reported zero capacity")
	}
	snapshot.Memory.CapacityBytes = capacity

	detected, err := detectPlatform(ctx, &snapshot)
	if err == nil {
		snapshot.Accelerators = finalizeAccelerators(detected)
	}

	if err := snapshot.Validate(); err != nil {
		return HardwareSnapshot{}, fmt.Errorf("validating detected hardware: %w", err)
	}
	return snapshot, nil
}

func normalizeArchitecture(raw string) Architecture {
	switch raw {
	case "amd64":
		return Architecture{Name: "x86_64", RawName: stringPtr(raw)}
	case "arm64", "ppc64le", "riscv64", "s390x":
		return Architecture{Name: raw}
	case "arm":
		return Architecture{Name: "armv7", RawName: stringPtr(raw)}
	default:
		return Architecture{Name: "other", RawName: stringPtr(raw)}
	}
}

func normalizeOS(raw string) string {
	switch raw {
	case "linux", "windows", "freebsd":
		return raw
	case "darwin":
		return "macos"
	default:
		return "other"
	}
}

func populateOperatingSystem(out *OperatingSystem, info *host.InfoStat) {
	if out.Family == "other" {
		out.RawFamily = nonEmptyStringPtr(runtime.GOOS)
	}
	name := strings.TrimSpace(info.Platform)
	if out.Family == "macos" {
		name = "macOS"
	} else if out.Family == "windows" && name == "" {
		name = "Windows"
	}
	out.Name = nonEmptyStringPtr(name)
	out.Version = nonEmptyStringPtr(info.PlatformVersion)
	out.Kernel = nonEmptyStringPtr(info.KernelVersion)
}

func populateCPU(ctx context.Context, out *CPU) {
	infos, _ := cpu.InfoWithContext(ctx)
	for _, info := range infos {
		if out.Vendor == nil {
			out.Vendor = nonEmptyStringPtr(info.VendorID)
		}
		if out.Model == nil {
			out.Model = nonEmptyStringPtr(info.ModelName)
		}
	}

	logical, logicalErr := cpu.CountsWithContext(ctx, true)
	if logicalErr == nil && logical > 0 {
		out.LogicalThreadCount = intPtr(logical)
	}
	physical, physicalErr := cpu.CountsWithContext(ctx, false)
	if physicalErr == nil && physical > 0 && (logical <= 0 || physical <= logical) {
		out.PhysicalCoreCount = intPtr(physical)
	}

	sockets := map[string]struct{}{}
	for _, info := range infos {
		if id := strings.TrimSpace(info.PhysicalID); id != "" {
			sockets[id] = struct{}{}
		}
	}
	if len(sockets) > 0 {
		out.SocketCount = intPtr(len(sockets))
	} else if (runtime.GOOS == "darwin" || runtime.GOOS == "windows") && len(infos) > 0 {
		out.SocketCount = intPtr(len(infos))
	}
}

func finalizeAccelerators(found []detectedAccelerator) []Accelerator {
	merged := make([]detectedAccelerator, 0, len(found))
	byIdentity := make(map[string]int)
	for _, candidate := range found {
		if candidate.identity != "" {
			if index, ok := byIdentity[candidate.identity]; ok {
				mergeAccelerator(&merged[index].value, candidate.value)
				continue
			}
			byIdentity[candidate.identity] = len(merged)
		}
		merged = append(merged, candidate)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		left, right := merged[i].identity, merged[j].identity
		if left != right {
			return left < right
		}
		return stringValue(merged[i].value.Model) < stringValue(merged[j].value.Model)
	})
	result := make([]Accelerator, len(merged))
	for i := range merged {
		result[i] = merged[i].value
		result[i].Index = i
	}
	return result
}

func mergeAccelerator(dst *Accelerator, src Accelerator) {
	if dst.Vendor == nil {
		dst.Vendor = src.Vendor
	}
	if dst.Model == nil {
		dst.Model = src.Model
	}
	if dst.Architecture == nil {
		dst.Architecture = src.Architecture
	}
	if dst.Memory.Kind == "" || dst.Memory.Kind == "unknown" {
		dst.Memory.Kind = src.Memory.Kind
	}
	if dst.Memory.CapacityBytes == nil {
		dst.Memory.CapacityBytes = src.Memory.CapacityBytes
	}
	if dst.Driver == nil {
		dst.Driver = src.Driver
	} else if src.Driver != nil {
		if dst.Driver.Name == nil {
			dst.Driver.Name = src.Driver.Name
		}
		if dst.Driver.Version == nil {
			dst.Driver.Version = src.Driver.Version
		}
	}
	if dst.PowerLimitWatts == nil {
		dst.PowerLimitWatts = src.PowerLimitWatts
	}
}

func stringPtr(value string) *string { return &value }

func nonEmptyStringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "n/a") || strings.EqualFold(value, "unknown") {
		return nil
	}
	return &value
}

func intPtr(value int) *int { return &value }

func uint64Ptr(value uint64) *uint64 { return &value }

func float64Ptr(value float64) *float64 { return &value }

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
