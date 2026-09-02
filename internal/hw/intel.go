package hw

// intelGPUInfo describes what we can infer about an Intel GPU from its PCI
// device ID.
type intelGPUInfo struct {
	// Architecture is the GPU generation codename (e.g. "Battlemage"). Always
	// set for a known device ID.
	Architecture string
	// Model is the marketing name (e.g. "Arc B570"). Empty when the device ID
	// maps to a die that ships under several SKU names and cannot be resolved
	// unambiguously (true for most integrated parts and the Alchemist dies).
	Model string
}

// Intel PCI device IDs grouped by platform. Sourced from the Linux kernel DRM
// header include/drm/intel/pciids.h (the INTEL_*_IDS macros) and Mesa's
// chipset tables. Refresh from those sources when Intel ships new IDs; unknown
// IDs simply return ok=false and leave the field "Not detected" rather than
// guessing.
var intelArchByID = func() map[uint16]string {
	m := map[uint16]string{}
	add := func(arch string, ids ...uint16) {
		for _, id := range ids {
			m[id] = arch
		}
	}

	// Discrete — DG1.
	add("DG1", 0x4905, 0x4906, 0x4907, 0x4908, 0x4909)

	// Discrete — Alchemist (Xe-HPG), DG2 dies G10/G11/G12 and ATS-M.
	add("Alchemist",
		// DG2 G10
		0x56A0, 0x56A1, 0x56A2, 0x56BE, 0x56BF, 0x5690, 0x5691, 0x5692,
		// DG2 G11
		0x56A5, 0x56A6, 0x56B0, 0x56B1, 0x56BA, 0x56BB, 0x56BC, 0x56BD,
		0x5693, 0x5694, 0x5695,
		// DG2 G12
		0x56A3, 0x56A4, 0x56B2, 0x56B3, 0x5696, 0x5697,
		// ATS-M
		0x56C0, 0x56C1, 0x56C2,
	)

	// Discrete — Battlemage (Xe2-HPG), BMG G21.
	add("Battlemage",
		0xE202, 0xE209, 0xE20B, 0xE20C, 0xE20D, 0xE210, 0xE211, 0xE212,
		0xE216, 0xE220, 0xE221, 0xE222, 0xE223,
	)

	// Integrated — Tiger Lake (Xe / Iris Xe).
	add("Tiger Lake",
		0x9A60, 0x9A68, 0x9A70, 0x9A40, 0x9A49, 0x9A59, 0x9A78,
		0x9AC0, 0x9AC9, 0x9AD9, 0x9AF8,
	)

	// Integrated — Rocket Lake.
	add("Rocket Lake", 0x4C80, 0x4C8A, 0x4C8B, 0x4C8C, 0x4C90, 0x4C9A)

	// Integrated — Alder Lake (S/P/N).
	add("Alder Lake",
		// ADL-S
		0x4680, 0x4682, 0x4688, 0x468A, 0x468B, 0x4690, 0x4692, 0x4693,
		// ADL-P
		0x46A0, 0x46A1, 0x46A2, 0x46A3, 0x46A6, 0x46A8, 0x46AA, 0x462A,
		0x4626, 0x4628, 0x46B0, 0x46B1, 0x46B2, 0x46B3, 0x46C0, 0x46C1,
		0x46C2, 0x46C3,
		// ADL-N
		0x46D0, 0x46D1, 0x46D2, 0x46D3, 0x46D4,
	)

	// Integrated — Meteor Lake (Xe-LPG).
	add("Meteor Lake", 0x7D40, 0x7D45, 0x7D55, 0x7D60, 0x7DD5)

	// Integrated — Arrow Lake.
	add("Arrow Lake", 0x7D51, 0x7DD1, 0x7D41, 0x7D67, 0xB640)

	// Integrated — Lunar Lake (Xe2-LPG).
	add("Lunar Lake", 0x6420, 0x64A0, 0x64B0)

	return m
}()

// intelModelByID maps device IDs to a marketing model only where a single die
// maps to a single well-documented product name. Alchemist dies and integrated
// parts are intentionally absent (one die ships under many SKU names).
var intelModelByID = map[uint16]string{
	0xE20B: "Arc B580",
	0xE20C: "Arc B570",
	0xE211: "Arc Pro B50",
	0xE212: "Arc Pro B60",
}

// intelGPU returns the architecture and (optional) model for a known Intel PCI
// device ID. ok is false for device IDs not in the table.
func intelGPU(deviceID uint16) (intelGPUInfo, bool) {
	arch, ok := intelArchByID[deviceID]
	if !ok {
		return intelGPUInfo{}, false
	}
	return intelGPUInfo{Architecture: arch, Model: intelModelByID[deviceID]}, true
}
