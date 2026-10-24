package perf

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/hw"
	"github.com/stretchr/testify/assert"
)

func gpu(vendor, memory string) hw.Accelerator {
	return hw.Accelerator{Kind: "gpu", Vendor: ptr(vendor), Memory: hw.AcceleratorMemory{Kind: memory}}
}

func TestPerf_AutoProbes(t *testing.T) {
	linuxAll := allProbes("linux")
	tests := []struct {
		name string
		goos string
		gpus []hw.Accelerator
		want []string
	}{
		{"apple", "darwin", []hw.Accelerator{gpu("Apple", "unified")}, []string{"mactop", "ioreg"}},
		{"nvidia discrete", "linux", []hw.Accelerator{gpu("NVIDIA", "dedicated"), gpu("NVIDIA", "dedicated")}, []string{"nvidia-smi"}},
		{"nvidia gb10", "linux", []hw.Accelerator{gpu("NVIDIA", "shared_system")}, []string{"nvidia-smi"}},
		{"amd apu", "linux", []hw.Accelerator{gpu("AMD", "unified")}, []string{"sysfs"}},
		{"amd discrete", "linux", []hw.Accelerator{gpu("AMD", "dedicated")}, []string{"rocm-smi", "lact", "sysfs"}},
		{"intel discrete", "linux", []hw.Accelerator{gpu("Intel", "dedicated")}, []string{"xpu-smi", "sysfs"}},
		{"intel igpu only", "linux", []hw.Accelerator{gpu("Intel", "shared_system")}, linuxAll},
		{"mixed amd nvidia", "linux", []hw.Accelerator{gpu("NVIDIA", "dedicated"), gpu("AMD", "dedicated")}, linuxAll},
		{"laptop igpu and dgpu", "linux", []hw.Accelerator{gpu("Intel", "shared_system"), gpu("NVIDIA", "dedicated")}, linuxAll},
		{"nothing detected", "linux", nil, linuxAll},
		{"windows nvidia", "windows", []hw.Accelerator{gpu("NVIDIA", "dedicated")}, []string{"nvidia-smi"}},
		{"windows amd", "windows", []hw.Accelerator{gpu("AMD", "dedicated")}, []string{"d3dkmt"}},
		{"windows mixed", "windows", []hw.Accelerator{gpu("NVIDIA", "dedicated"), gpu("Intel", "shared_system")}, []string{"nvidia-smi", "d3dkmt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, autoProbes(tt.goos, &hw.HardwareSnapshot{Accelerators: tt.gpus}))
		})
	}
	assert.Equal(t, linuxAll, autoProbes("linux", nil))
}
