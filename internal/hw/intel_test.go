package hw

import "testing"

func TestIntelGPU_DiscreteWithModel(t *testing.T) {
	tests := map[uint16]intelGPUInfo{
		0xE20B: {Architecture: "Battlemage", Model: "Arc B580"},
		0xE20C: {Architecture: "Battlemage", Model: "Arc B570"},
		0xE211: {Architecture: "Battlemage", Model: "Arc Pro B50"},
		0xE212: {Architecture: "Battlemage", Model: "Arc Pro B60"},
	}
	for id, want := range tests {
		got, ok := intelGPU(id)
		if !ok || got != want {
			t.Errorf("intelGPU(%#x) = %+v, %v; want %+v, true", id, got, ok, want)
		}
	}
}

func TestIntelGPU_BattlemageArchOnly(t *testing.T) {
	// Known Battlemage die IDs without a resolved marketing model.
	for _, id := range []uint16{0xE202, 0xE209, 0xE20D, 0xE210, 0xE216, 0xE220, 0xE223} {
		got, ok := intelGPU(id)
		if !ok {
			t.Fatalf("intelGPU(%#x) not found", id)
		}
		if got.Architecture != "Battlemage" || got.Model != "" {
			t.Errorf("intelGPU(%#x) = %+v; want {Battlemage, \"\"}", id, got)
		}
	}
}

func TestIntelGPU_AlchemistDG2(t *testing.T) {
	// One ID from each DG2 die plus ATS-M; architecture only, no model.
	for _, id := range []uint16{0x56A0, 0x5695, 0x5696, 0x56C1} {
		got, ok := intelGPU(id)
		if !ok || got.Architecture != "Alchemist" || got.Model != "" {
			t.Errorf("intelGPU(%#x) = %+v, %v; want {Alchemist, \"\"}, true", id, got, ok)
		}
	}
}

func TestIntelGPU_Integrated(t *testing.T) {
	tests := map[uint16]string{
		0x4909: "DG1",
		0x9A49: "Tiger Lake",
		0x4C8A: "Rocket Lake",
		0x4680: "Alder Lake", // ADL-S
		0x46A0: "Alder Lake", // ADL-P
		0x46D0: "Alder Lake", // ADL-N
		0x7D55: "Meteor Lake",
		0xB640: "Arrow Lake",
		0x64A0: "Lunar Lake",
	}
	for id, arch := range tests {
		got, ok := intelGPU(id)
		if !ok || got.Architecture != arch || got.Model != "" {
			t.Errorf("intelGPU(%#x) = %+v, %v; want {%s, \"\"}, true", id, got, ok, arch)
		}
	}
}

func TestIntelGPU_Unknown(t *testing.T) {
	// IDs adjacent to known ranges and zero must miss cleanly.
	for _, id := range []uint16{0x0000, 0xFFFF, 0x4904, 0x490A, 0xE201, 0xE224, 0x56C3} {
		if got, ok := intelGPU(id); ok {
			t.Errorf("intelGPU(%#x) = %+v, true; want miss", id, got)
		}
	}
}
