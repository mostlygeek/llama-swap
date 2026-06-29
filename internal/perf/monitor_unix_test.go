//go:build unix && !darwin

package perf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile writes a sysfs-style file, creating parent dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// makeAmdgpuCard creates a fake amdgpu card directory under root.
func makeAmdgpuCard(t *testing.T, root, card string, vramTotal, vramUsed, gttTotal, gttUsed string) {
	t.Helper()
	dev := filepath.Join(root, card, "device")
	if vramTotal != "" {
		writeFile(t, filepath.Join(dev, "mem_info_vram_total"), vramTotal)
	}
	if vramUsed != "" {
		writeFile(t, filepath.Join(dev, "mem_info_vram_used"), vramUsed)
	}
	if gttTotal != "" {
		writeFile(t, filepath.Join(dev, "mem_info_gtt_total"), gttTotal)
	}
	if gttUsed != "" {
		writeFile(t, filepath.Join(dev, "mem_info_gtt_used"), gttUsed)
	}
	writeFile(t, filepath.Join(dev, "uevent"), "DRIVER=amdgpu\nPCI_ID=1002:1681\n")
	writeFile(t, filepath.Join(dev, "gpu_busy_percent"), "42")
}

func TestReadSysfs_APU_CombinesVramAndGtt(t *testing.T) {
	root := t.TempDir()
	// APU profile from a Radeon 680M: 512 MiB VRAM carveout, ~23 GiB GTT,
	// ~13.8 GiB GTT used. The model lives in GTT, not VRAM.
	const mib = 1024 * 1024
	makeAmdgpuCard(t, root, "card0",
		"536870912",   // vram_total = 512 MiB
		"519950336",   // vram_used  ~ 496 MiB
		"24696061952", // gtt_total ~ 23552 MiB
		"14810259456", // gtt_used  ~ 14124 MiB
	)
	// Connector entries must be ignored.
	writeFile(t, filepath.Join(root, "card0-DP-1", "device", "uevent"), "DRIVER=amdgpu\n")
	writeFile(t, filepath.Join(root, "card0-HDMI-A-1", "device", "uevent"), "DRIVER=amdgpu\n")

	old := drmClassPath
	drmClassPath = root
	defer func() { drmClassPath = old }()

	stats, err := readSysfs()
	require.NoError(t, err)
	require.Len(t, stats, 1)

	s := stats[0]
	assert.Equal(t, 0, s.ID)
	assert.Equal(t, int((536870912+24696061952)/mib), s.MemTotalMB)
	assert.Equal(t, int((519950336+14810259456)/mib), s.MemUsedMB)
	// Used should reflect the real GTT working set (~14 GiB), not 512 MiB.
	assert.Greater(t, s.MemUsedMB, 13000)
	assert.Greater(t, s.MemTotalMB, 23000)
	assert.InDelta(t, float64(519950336+14810259456)/float64(536870912+24696061952)*100, s.MemUtilPct, 0.01)
	assert.Equal(t, float64(42), s.GpuUtilPct)
	assert.Contains(t, s.Name, "1002:1681")
}

func TestReadSysfs_SkipsNonAmdgpu(t *testing.T) {
	root := t.TempDir()

	// A non-amdgpu card (no mem_info_gtt_total) must be skipped.
	dev := filepath.Join(root, "card0", "device")
	writeFile(t, filepath.Join(dev, "uevent"), "DRIVER=i915\n")
	writeFile(t, filepath.Join(dev, "mem_info_vram_total"), "1073741824")

	old := drmClassPath
	drmClassPath = root
	defer func() { drmClassPath = old }()

	stats, err := readSysfs()
	require.ErrorIs(t, err, ErrNoGpuTool)
	require.Nil(t, stats)
}

func TestReadSysfs_DGPU_VramDominates(t *testing.T) {
	root := t.TempDir()
	const mib = 1024 * 1024
	// dGPU: 24 GiB VRAM, small GTT. Sum is dominated by VRAM, matching
	// what rocm-smi/nvidia-smi would report.
	makeAmdgpuCard(t, root, "card1",
		"25769803776", // vram_total = 24 GiB
		"10737418240", // vram_used  = 10 GiB
		"268435456",   // gtt_total  = 256 MiB
		"16777216",    // gtt_used   = 16 MiB
	)

	old := drmClassPath
	drmClassPath = root
	defer func() { drmClassPath = old }()

	stats, err := readSysfs()
	require.NoError(t, err)
	require.Len(t, stats, 1)

	s := stats[0]
	assert.Equal(t, 1, s.ID)
	assert.Equal(t, int((25769803776+268435456)/mib), s.MemTotalMB)
	assert.Equal(t, int((10737418240+16777216)/mib), s.MemUsedMB)
}

func TestReadSysfs_MissingFilesGraceful(t *testing.T) {
	root := t.TempDir()
	// gtt_total present but vram_* and gtt_used missing: should still report
	// what it can without erroring.
	dev := filepath.Join(root, "card0", "device")
	writeFile(t, filepath.Join(dev, "mem_info_gtt_total"), "24696061952")
	writeFile(t, filepath.Join(dev, "uevent"), "DRIVER=amdgpu\n")

	old := drmClassPath
	drmClassPath = root
	defer func() { drmClassPath = old }()

	stats, err := readSysfs()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Greater(t, stats[0].MemTotalMB, 0)
	assert.Equal(t, 0, stats[0].MemUsedMB)
}

func TestSysfsHasApuCarveout_APUDetected(t *testing.T) {
	root := t.TempDir()
	// Radeon 680M: 512 MiB VRAM carveout, 23 GiB GTT. rocm-smi would only see
	// the carveout, so we must defer to sysfs.
	makeAmdgpuCard(t, root, "card0",
		"536870912",   // vram_total = 512 MiB
		"519950336",   // vram_used
		"24696061952", // gtt_total ~ 23 GiB
		"14810259456", // gtt_used
	)

	old := drmClassPath
	drmClassPath = root
	defer func() { drmClassPath = old }()

	assert.True(t, sysfsHasApuCarveout(),
		"APU with tiny VRAM carveout and large GTT should be detected")
}

func TestSysfsHasApuCarveout_LargeUMACarveoutDetected(t *testing.T) {
	root := t.TempDir()
	// M3 regression: some 680M/780M boards expose a 2-4 GiB BIOS UMA carveout,
	// not the typical 512 MiB. The old absolute 1 GiB VRAM bound missed these,
	// so rocm-smi (carveout-only) wrongly won. The ratio discriminator
	// (gtt >= 2*vram) detects them regardless of carveout size.
	makeAmdgpuCard(t, root, "card0",
		"4294967296",  // vram_total = 4 GiB UMA carveout
		"2147483648",  // vram_used  = 2 GiB
		"30064771072", // gtt_total  = 28 GiB (dwarfs the 4 GiB carveout)
		"10737418240", // gtt_used   = 10 GiB
	)

	old := drmClassPath
	drmClassPath = root
	defer func() { drmClassPath = old }()

	assert.True(t, sysfsHasApuCarveout(),
		"APU with a 4 GiB UMA carveout and far larger GTT must be detected via the ratio")
}

func TestSysfsHasApuCarveout_DGPUNotDetected(t *testing.T) {
	root := t.TempDir()
	// dGPU: 24 GiB real VRAM, small GTT. rocm-smi is authoritative here, so
	// this must NOT trigger the deferral.
	makeAmdgpuCard(t, root, "card0",
		"25769803776", // vram_total = 24 GiB
		"10737418240", // vram_used
		"268435456",   // gtt_total = 256 MiB
		"16777216",    // gtt_used
	)

	old := drmClassPath
	drmClassPath = root
	defer func() { drmClassPath = old }()

	assert.False(t, sysfsHasApuCarveout(),
		"dGPU with large VRAM should not be treated as an APU carveout")
}

func TestSysfsHasApuCarveout_NoAmdgpu(t *testing.T) {
	root := t.TempDir()
	// Non-amdgpu card: no mem_info_gtt_total. Nothing to defer for.
	dev := filepath.Join(root, "card0", "device")
	writeFile(t, filepath.Join(dev, "uevent"), "DRIVER=i915\n")
	writeFile(t, filepath.Join(dev, "mem_info_vram_total"), "536870912")

	old := drmClassPath
	drmClassPath = root
	defer func() { drmClassPath = old }()

	assert.False(t, sysfsHasApuCarveout())
}

func TestSysfsHasApuCarveout_SkipsConnectorEntries(t *testing.T) {
	root := t.TempDir()
	makeAmdgpuCard(t, root, "card0",
		"536870912", "519950336", "24696061952", "14810259456")
	// Connector nodes also match card* but have no memory accounting; they
	// must not affect the result.
	writeFile(t, filepath.Join(root, "card0-DP-1", "device", "uevent"), "DRIVER=amdgpu\n")

	old := drmClassPath
	drmClassPath = root
	defer func() { drmClassPath = old }()

	assert.True(t, sysfsHasApuCarveout())
}
