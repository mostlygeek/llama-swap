package hw

import (
	"fmt"
	"math"
	"strings"
)

func (s HardwareSnapshot) Validate() error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %d", SchemaVersion)
	}
	if s.CapturedAt.IsZero() {
		return fmt.Errorf("captured_at is required")
	}
	if s.Capture.Scope != CaptureScopeInferenceHost {
		return fmt.Errorf("capture.scope must be %q", CaptureScopeInferenceHost)
	}
	if s.Capture.Method != CaptureMethodDetected {
		return fmt.Errorf("capture.method must be %q", CaptureMethodDetected)
	}
	if s.Capture.Detector == nil || strings.TrimSpace(s.Capture.Detector.Name) == "" || strings.TrimSpace(s.Capture.Detector.Version) == "" {
		return fmt.Errorf("capture.detector name and version are required")
	}
	if err := validateNamedValue("architecture", s.Architecture.Name, s.Architecture.RawName,
		"x86_64", "arm64", "armv7", "ppc64le", "riscv64", "s390x", "other"); err != nil {
		return err
	}
	if err := validateNamedValue("operating_system.family", s.OperatingSystem.Family, s.OperatingSystem.RawFamily,
		"linux", "macos", "windows", "freebsd", "other"); err != nil {
		return err
	}
	if err := validateNamedValue("environment.kind", s.Environment.Kind, s.Environment.RawKind,
		"native", "container", "virtual_machine", "wsl", "other", "unknown"); err != nil {
		return err
	}
	if s.Memory.CapacityBytes == 0 {
		return fmt.Errorf("memory.capacity_bytes must be positive")
	}
	for name, value := range map[string]*int{
		"cpu.socket_count":         s.CPU.SocketCount,
		"cpu.physical_core_count":  s.CPU.PhysicalCoreCount,
		"cpu.logical_thread_count": s.CPU.LogicalThreadCount,
	} {
		if value != nil && *value <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	if s.CPU.PhysicalCoreCount != nil && s.CPU.LogicalThreadCount != nil && *s.CPU.LogicalThreadCount < *s.CPU.PhysicalCoreCount {
		return fmt.Errorf("cpu.logical_thread_count must be greater than or equal to physical_core_count")
	}

	for i, accelerator := range s.Accelerators {
		path := fmt.Sprintf("accelerators[%d]", i)
		if accelerator.Index != i {
			return fmt.Errorf("%s.index must be %d", path, i)
		}
		if err := validateNamedValue(path+".kind", accelerator.Kind, accelerator.RawKind, "gpu", "npu", "other"); err != nil {
			return err
		}
		switch accelerator.Memory.Kind {
		case "dedicated", "unified", "shared_system", "unknown":
		default:
			return fmt.Errorf("%s.memory.kind is invalid", path)
		}
		if accelerator.Memory.CapacityBytes != nil && *accelerator.Memory.CapacityBytes == 0 {
			return fmt.Errorf("%s.memory.capacity_bytes must be positive", path)
		}
		if accelerator.PowerLimitWatts != nil && (*accelerator.PowerLimitWatts <= 0 || math.IsNaN(*accelerator.PowerLimitWatts) || math.IsInf(*accelerator.PowerLimitWatts, 0)) {
			return fmt.Errorf("%s.power_limit_watts must be positive and finite", path)
		}
	}
	return nil
}

func validateNamedValue(path, value string, raw *string, allowed ...string) error {
	valid := false
	for _, candidate := range allowed {
		if value == candidate {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("%s is invalid", path)
	}
	if value == "other" && (raw == nil || strings.TrimSpace(*raw) == "") {
		return fmt.Errorf("%s raw value is required for other", path)
	}
	return nil
}
