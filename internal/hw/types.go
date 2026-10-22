package hw

import "time"

const SchemaVersion = 1

const (
	CaptureScopeInferenceHost = "inference_host"
	CaptureMethodDetected     = "detected"
)

type HardwareSnapshot struct {
	SchemaVersion   int                  `json:"schema_version"`
	CapturedAt      time.Time            `json:"captured_at"`
	Capture         HardwareCapture      `json:"capture"`
	Architecture    Architecture         `json:"architecture"`
	OperatingSystem OperatingSystem      `json:"operating_system"`
	System          System               `json:"system"`
	Environment     ExecutionEnvironment `json:"environment"`
	CPU             CPU                  `json:"cpu"`
	Memory          SystemMemory         `json:"memory"`
	Accelerators    []Accelerator        `json:"accelerators"`
}

// System identifies the physical machine from DMI product data. A nil field
// means the value is not exposed by the platform.
type System struct {
	Vendor *string `json:"vendor"`
	Model  *string `json:"model"`
	Family *string `json:"family"`
}

type HardwareCapture struct {
	Scope    string        `json:"scope"`
	Method   string        `json:"method"`
	Detector *DetectorInfo `json:"detector"`
}

type DetectorInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Architecture struct {
	Name    string  `json:"name"`
	RawName *string `json:"raw_name"`
}

type OperatingSystem struct {
	Family    string  `json:"family"`
	Name      *string `json:"name"`
	Version   *string `json:"version"`
	Kernel    *string `json:"kernel"`
	RawFamily *string `json:"raw_family,omitempty"`
}

type ExecutionEnvironment struct {
	Kind    string  `json:"kind"`
	Name    *string `json:"name"`
	Version *string `json:"version"`
	RawKind *string `json:"raw_kind,omitempty"`
}

type CPU struct {
	Vendor             *string `json:"vendor"`
	Model              *string `json:"model"`
	SocketCount        *int    `json:"socket_count"`
	PhysicalCoreCount  *int    `json:"physical_core_count"`
	LogicalThreadCount *int    `json:"logical_thread_count"`
}

type SystemMemory struct {
	CapacityBytes uint64 `json:"capacity_bytes"`
}

type Accelerator struct {
	Index           int               `json:"index"`
	Kind            string            `json:"kind"`
	RawKind         *string           `json:"raw_kind,omitempty"`
	Vendor          *string           `json:"vendor"`
	Model           *string           `json:"model"`
	Architecture    *string           `json:"architecture"`
	Memory          AcceleratorMemory `json:"memory"`
	Driver          *Driver           `json:"driver"`
	PowerLimitWatts *float64          `json:"power_limit_watts"`
	// NominalPowerWatts is a vendor-documented nominal power figure for the
	// accelerator or its SoC, reported when the platform exposes no power
	// limit. It is a design figure, not an enforced limit.
	NominalPowerWatts *float64 `json:"nominal_power_watts"`
}

type AcceleratorMemory struct {
	Kind          string  `json:"kind"`
	CapacityBytes *uint64 `json:"capacity_bytes"`
}

type Driver struct {
	Name    *string `json:"name"`
	Version *string `json:"version"`
}

type detectedAccelerator struct {
	identity string
	value    Accelerator
}
