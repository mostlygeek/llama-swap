package hw

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHardware_ArchitectureNormalization(t *testing.T) {
	tests := []struct {
		raw     string
		name    string
		rawName string
	}{
		{raw: "amd64", name: "x86_64", rawName: "amd64"},
		{raw: "arm64", name: "arm64"},
		{raw: "arm", name: "armv7", rawName: "arm"},
		{raw: "mips64", name: "other", rawName: "mips64"},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			got := normalizeArchitecture(test.raw)
			if got.Name != test.name || stringValue(got.RawName) != test.rawName {
				t.Fatalf("normalizeArchitecture(%q) = %+v", test.raw, got)
			}
		})
	}
}

func TestHardware_ValidateValidSnapshot(t *testing.T) {
	snapshot := validTestSnapshot()
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestHardware_ValidateRejectsInvalidCounts(t *testing.T) {
	snapshot := validTestSnapshot()
	physical, logical := 16, 8
	snapshot.CPU.PhysicalCoreCount = &physical
	snapshot.CPU.LogicalThreadCount = &logical
	if err := snapshot.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want invalid CPU counts")
	}
}

func TestHardware_FinalizeAcceleratorsMergesAndRemovesIdentity(t *testing.T) {
	capacity := uint64(24 * 1024 * 1024 * 1024)
	found := []detectedAccelerator{
		{identity: "0000:02:00.0", value: Accelerator{Kind: "gpu", Vendor: stringPtr("NVIDIA"), Memory: AcceleratorMemory{Kind: "dedicated", CapacityBytes: &capacity}}},
		{identity: "0000:02:00.0", value: Accelerator{Kind: "gpu", Model: stringPtr("RTX 4090"), Memory: AcceleratorMemory{Kind: "unknown"}}},
	}
	got := finalizeAccelerators(found)
	if len(got) != 1 || got[0].Index != 0 || stringValue(got[0].Model) != "RTX 4090" {
		t.Fatalf("finalizeAccelerators() = %+v", got)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || strings.Contains(string(data), "0000:02:00.0") || strings.Contains(string(data), "uuid") || strings.Contains(string(data), "bus_id") {
		t.Fatalf("serialized accelerators leak identity: %s", data)
	}
}

func TestHardware_FinalizeAcceleratorsPreservesMixedVendors(t *testing.T) {
	capacity := uint64(32 * 1024 * 1024 * 1024)
	found := []detectedAccelerator{
		{identity: "0000:01:00.0", value: Accelerator{Kind: "gpu", Vendor: stringPtr("NVIDIA"), Model: stringPtr("RTX 4090"), Memory: AcceleratorMemory{Kind: "dedicated"}}},
		{identity: "0000:65:00.0", value: Accelerator{Kind: "gpu", Vendor: stringPtr("AMD"), Architecture: stringPtr("gfx1151"), Memory: AcceleratorMemory{Kind: "unified"}}},
		{identity: "0000:65:00.0", value: Accelerator{Kind: "gpu", Vendor: stringPtr("AMD"), Model: stringPtr("Radeon 8060S"), Memory: AcceleratorMemory{Kind: "dedicated", CapacityBytes: &capacity}}},
	}

	got := finalizeAccelerators(found)
	if len(got) != 2 {
		t.Fatalf("finalizeAccelerators() returned %d devices, want 2: %+v", len(got), got)
	}
	if stringValue(got[0].Vendor) != "NVIDIA" || stringValue(got[1].Vendor) != "AMD" {
		t.Fatalf("finalizeAccelerators() vendors = %q, %q", stringValue(got[0].Vendor), stringValue(got[1].Vendor))
	}
	if stringValue(got[1].Architecture) != "gfx1151" || stringValue(got[1].Model) != "Radeon 8060S" {
		t.Fatalf("finalizeAccelerators() AMD device = %+v", got[1])
	}
	if got[1].Memory.Kind != "unified" || got[1].Memory.CapacityBytes == nil || *got[1].Memory.CapacityBytes != capacity {
		t.Fatalf("finalizeAccelerators() AMD memory = %+v", got[1].Memory)
	}
}

func TestHardware_DetectLocalHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := Detect(ctx, "test")
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("detected snapshot is invalid: %v", err)
	}
}

func TestHardware_ParseNvidiaCSV(t *testing.T) {
	records, err := parseNvidiaCSV("0, NVIDIA GeForce RTX 4090, GPU-abc, 00000000:01:00.0, 24564, 570.124.06, 450.00\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].memoryBytes != 24564*1024*1024 || records[0].powerLimit != 450 {
		t.Fatalf("parseNvidiaCSV() = %+v", records)
	}
}

func TestHardware_ParseNvidiaCSVGB10(t *testing.T) {
	// GB10 (DGX Spark class) drivers report [N/A] for both memory.total and
	// power.limit; both must parse as absent rather than as errors.
	output := "0, NVIDIA GB10, GPU-e69daebb-45de-2588-13c3-3f623ae44eaa, 0000000F:01:00.0, [N/A], 580.178.04, [N/A]\n"
	records, err := parseNvidiaCSV(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].name != "NVIDIA GB10" || records[0].memoryBytes != 0 || records[0].powerLimit != 0 {
		t.Fatalf("parseNvidiaCSV() = %+v", records)
	}
}

func TestHardware_IsGB10(t *testing.T) {
	if !isGB10("NVIDIA GB10") || !isGB10("nvidia gb10 superchip") {
		t.Fatal("isGB10() = false for GB10 names, want true")
	}
	if isGB10("NVIDIA GeForce RTX 4090") || isGB10("Tesla H100") || isGB10("") {
		t.Fatal("isGB10() = true for non-GB10 names, want false")
	}
}

func TestHardware_NvidiaMemory(t *testing.T) {
	tests := []struct {
		name        string
		gb10        bool
		memoryBytes uint64
		systemBytes uint64
		kind        string
		capacity    uint64
	}{
		{name: "dedicated memory", gb10: false, memoryBytes: 24564 * 1024 * 1024, systemBytes: 32 * 1024 * 1024 * 1024, kind: "dedicated", capacity: 24564 * 1024 * 1024},
		{name: "gb10 unified memory", gb10: true, memoryBytes: 0, systemBytes: 137040367616, kind: "shared_system", capacity: 137040367616},
		{name: "gb10 without system total", gb10: true, memoryBytes: 0, kind: "shared_system"},
		{name: "memory not reported", gb10: false, memoryBytes: 0, systemBytes: 32 * 1024 * 1024 * 1024, kind: "dedicated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := nvidiaMemory(test.gb10, test.memoryBytes, test.systemBytes)
			if got.Kind != test.kind {
				t.Fatalf("nvidiaMemory().Kind = %q, want %q", got.Kind, test.kind)
			}
			if (got.CapacityBytes == nil) != (test.capacity == 0) {
				t.Fatalf("nvidiaMemory().CapacityBytes = %v, want nil=%v", got.CapacityBytes, test.capacity == 0)
			}
			if got.CapacityBytes != nil && *got.CapacityBytes != test.capacity {
				t.Fatalf("nvidiaMemory().CapacityBytes = %d, want %d", *got.CapacityBytes, test.capacity)
			}
		})
	}
}

func TestHardware_NvidiaPowerLimit(t *testing.T) {
	tests := []struct {
		reported float64
		name     string
		want     float64
		wantNil  bool
	}{
		{reported: 450, name: "NVIDIA GeForce RTX 4090", want: 450},
		{reported: 95, name: "NVIDIA GB10", want: 95},
		{reported: 0, name: "NVIDIA GB10", want: gb10NominalPowerLimitWatts},
		{reported: 0, name: "NVIDIA GeForce RTX 4090", wantNil: true},
	}
	for _, test := range tests {
		got := nvidiaPowerLimit(test.reported, test.name)
		if test.wantNil {
			if got != nil {
				t.Fatalf("nvidiaPowerLimit(%v, %q) = %v, want nil", test.reported, test.name, *got)
			}
			continue
		}
		if got == nil || *got != test.want {
			t.Fatalf("nvidiaPowerLimit(%v, %q) = %v, want %v", test.reported, test.name, got, test.want)
		}
	}
}

func TestHardware_GB10CPUModelLabel(t *testing.T) {
	tests := []struct {
		name   string
		models []cpuModelCount
		want   string
	}{
		{name: "single model", models: []cpuModelCount{{name: "Cortex-A725", count: 20}}, want: ""},
		{name: "empty", models: nil, want: ""},
		{name: "gb10 layout", models: []cpuModelCount{{name: "Cortex-A725", count: 10}, {name: "Cortex-X925", count: 10}}, want: "NVIDIA GB10 Grace CPU (10 Cortex-X925 + 10 Cortex-A725 cores)"},
		{name: "gb10 layout reversed", models: []cpuModelCount{{name: "Cortex-X925", count: 10}, {name: "Cortex-A725", count: 10}}, want: "NVIDIA GB10 Grace CPU (10 Cortex-X925 + 10 Cortex-A725 cores)"},
		{name: "other hybrid", models: []cpuModelCount{{name: "Cortex-X4", count: 2}, {name: "Cortex-A720", count: 6}}, want: "2 Cortex-X4 + 6 Cortex-A720"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := gb10CPUModelLabel(test.models); got != test.want {
				t.Fatalf("gb10CPUModelLabel() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHardware_NvidiaComputeCapabilityArchitectures(t *testing.T) {
	records := []nvidiaRecord{
		{index: 0, name: "Tesla P40"},
		{index: 1, name: "Tesla P40"},
		{index: 2, name: "NVIDIA GeForce RTX 3090"},
		{index: 3, name: "NVIDIA GeForce RTX 3090", architecture: "Ampere"},
	}
	applyNvidiaComputeCapabilities(records, "0, 6.1\n1, 6.1\n2, 8.6\n3, 8.6\n")

	want := []string{"Pascal", "Pascal", "Ampere", "Ampere"}
	for i := range records {
		if records[i].architecture != want[i] {
			t.Errorf("records[%d].architecture = %q, want %q", i, records[i].architecture, want[i])
		}
	}
}

func validTestSnapshot() HardwareSnapshot {
	return HardwareSnapshot{
		SchemaVersion: SchemaVersion,
		CapturedAt:    time.Now().UTC(),
		Capture: HardwareCapture{
			Scope:    CaptureScopeInferenceHost,
			Method:   CaptureMethodDetected,
			Detector: &DetectorInfo{Name: "llama-swap", Version: "test"},
		},
		Architecture:    Architecture{Name: "x86_64"},
		OperatingSystem: OperatingSystem{Family: "linux"},
		Environment:     ExecutionEnvironment{Kind: "unknown"},
		Memory:          SystemMemory{CapacityBytes: 1024},
		Accelerators:    []Accelerator{},
	}
}
