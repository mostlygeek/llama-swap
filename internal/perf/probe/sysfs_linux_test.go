package probe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixture mirrors an Intel Arc Pro B70 (xe driver) on kernel 7.1: hwmon
// temps labelled pkg/vram, an energy counter, fan RPM and max, a 32 GiB VRAM
// BAR, and fdinfo in the cycles format. Ported from #1159.
const (
	xeDir   = "devices/pci0000:00/0000:00:01.0/0000:01:00.0/0000:03:00.0"
	igpuDir = "devices/pci0000:00/0000:00:02.0"
	amdDir  = "devices/pci0000:00/0000:00:08.1/0000:c3:00.0"
)

const xeFdinfo = `pos:	0
flags:	02000002
drm-driver:	xe
drm-client-id:	20
drm-pdev:	0000:03:00.0
drm-total-vram0:	16946852 KiB
drm-resident-vram0:	16000 MiB
drm-cycles-rcs:	744
drm-total-cycles-rcs:	18956600119
drm-cycles-ccs:	386293167
drm-total-cycles-ccs:	18956600119
`

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	}
}

func symlink(t *testing.T, root, link, target string) {
	t.Helper()
	path := filepath.Join(root, link)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.Symlink(target, path))
}

// fakeSysfs builds a sysfs tree with an xe dGPU (with hwmon) and a bare i915
// iGPU (no hwmon, so the sysfs probe skips it), and points sysfsRoot at it.
func fakeSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		xeDir + "/class":                      "0x030000",
		xeDir + "/vendor":                     "0x8086",
		xeDir + "/device":                     "0xe223",
		xeDir + "/power/runtime_status":       "active",
		xeDir + "/resource":                   "0x00000041f8000000 0x00000041f8ffffff 0x000000000014220c\n0x0000004800000000 0x0000004fffffffff 0x000000000014220c\n",
		xeDir + "/hwmon/hwmon9/temp2_input":   "48000",
		xeDir + "/hwmon/hwmon9/temp2_label":   "pkg",
		xeDir + "/hwmon/hwmon9/temp3_input":   "50000",
		xeDir + "/hwmon/hwmon9/temp3_label":   "vram",
		xeDir + "/hwmon/hwmon9/fan1_input":    "1074",
		xeDir + "/hwmon/hwmon9/fan1_max":      "4980",
		xeDir + "/hwmon/hwmon9/energy1_input": "255045692199",
		igpuDir + "/class":                    "0x030000",
		igpuDir + "/vendor":                   "0x8086",
		igpuDir + "/device":                   "0x4692",
		igpuDir + "/power/runtime_status":     "suspended",
	})
	for _, d := range []string{"xe", "i915", "amdgpu"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "bus/pci/drivers", d), 0755))
	}
	symlink(t, root, xeDir+"/driver", "../../../../../bus/pci/drivers/xe")
	symlink(t, root, igpuDir+"/driver", "../../../bus/pci/drivers/i915")
	symlink(t, root, "class/drm/card0/device", "../../../"+xeDir)
	symlink(t, root, "class/drm/card0-DP-1/device", "../../../"+xeDir)
	symlink(t, root, "class/drm/card1/device", "../../../"+igpuDir)
	symlink(t, root, "class/drm/renderD128/device", "../../../"+xeDir)
	symlink(t, root, "bus/pci/devices/0000:03:00.0", "../../../"+xeDir)
	symlink(t, root, "bus/pci/devices/0000:00:02.0", "../../../"+igpuDir)

	old := sysfsRoot
	sysfsRoot = root
	t.Cleanup(func() { sysfsRoot = old })
	return root
}

// addAmdAPU adds an amdgpu APU with a 512 MiB carveout and a large GTT pool.
func addAmdAPU(t *testing.T, root, runtimeStatus string) {
	t.Helper()
	writeFiles(t, root, map[string]string{
		amdDir + "/class":                "0x038000",
		amdDir + "/vendor":               "0x1002",
		amdDir + "/device":               "0x1586",
		amdDir + "/power/runtime_status": runtimeStatus,
		amdDir + "/gpu_busy_percent":     "97",
		amdDir + "/mem_info_vram_total":  "536870912",
		amdDir + "/mem_info_vram_used":   "91729920",
		amdDir + "/mem_info_gtt_total":   "103079215104",
		amdDir + "/mem_info_gtt_used":    "68719476736",
	})
	symlink(t, root, amdDir+"/driver", "../../../../bus/pci/drivers/amdgpu")
	symlink(t, root, "class/drm/card2/device", "../../../"+amdDir)
	symlink(t, root, "bus/pci/devices/0000:c3:00.0", "../../../"+amdDir)
}

// fakeProc points procRoot at a tree where pid 1234 holds the xe render node.
func fakeProc(t *testing.T, fdinfo string) string {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"1234/fdinfo/17": fdinfo})
	symlink(t, root, "1234/fd/17", "/dev/dri/renderD128")
	old := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = old })
	return root
}

func TestProbe_SysfsDiscoverSkipsCardsWithoutSensors(t *testing.T) {
	fakeSysfs(t)
	cards := discoverSysfsCards()
	require.Len(t, cards, 1)
	assert.Equal(t, "0000:03:00.0", cards[0].key)
	assert.Equal(t, "xe", cards[0].driver)
	assert.Equal(t, "Intel xe 0xe223", cards[0].name)
}

func TestProbe_SysfsReadsHwmonAndFdinfo(t *testing.T) {
	fakeSysfs(t)
	proc := fakeProc(t, xeFdinfo)
	p := newSysfs(Options{SkipSuspended: true})

	samples, err := p.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 1)
	s := samples[0]
	assert.Equal(t, GpuStateOK, s.State)
	assert.Equal(t, 48.0, *s.TempC)
	assert.Equal(t, 50.0, *s.VramTempC)
	assert.InDelta(t, 21.57, *s.FanPct, 0.01)
	assert.Equal(t, 32768, *s.VramTotalMB, "from the PCI BAR")
	assert.Equal(t, 16549, *s.VramUsedMB, "drm-total-vram0 in KiB")
	assert.Nil(t, s.UtilPct, "utilization needs two reads")
	assert.Nil(t, s.PowerW, "energy power needs two reads")

	// advance the engine counters: ccs went from 386293167 to 1386293167
	// cycles of 10000000000 total, so 10% busy
	writeFiles(t, proc, map[string]string{"1234/fdinfo/17": `drm-client-id:	20
drm-pdev:	0000:03:00.0
drm-cycles-ccs:	1386293167
drm-total-cycles-ccs:	28956600119
`})
	samples, err = p.Read(context.Background())
	require.NoError(t, err)
	require.NotNil(t, samples[0].UtilPct)
	assert.InDelta(t, 10.0, *samples[0].UtilPct, 0.01)
}

func TestProbe_SysfsAmdAPUReportsGTTAsShared(t *testing.T) {
	root := fakeSysfs(t)
	addAmdAPU(t, root, "active")
	fakeProc(t, "")

	samples, err := newSysfs(Options{}).Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 2)
	s := samples[1]
	assert.Equal(t, "0000:c3:00.0", s.Key)
	assert.Equal(t, 97.0, *s.UtilPct)
	assert.Equal(t, 512, *s.VramTotalMB)
	assert.Equal(t, 87, *s.VramUsedMB)
	assert.Equal(t, 98304, *s.SharedTotalMB)
	assert.Equal(t, 65536, *s.SharedUsedMB)
}

func TestProbe_SysfsSkipsSuspendedCards(t *testing.T) {
	root := fakeSysfs(t)
	addAmdAPU(t, root, "suspended")
	fakeProc(t, "")

	samples, err := newSysfs(Options{SkipSuspended: true}).Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 2)
	assert.Equal(t, GpuStateSuspended, samples[1].State)
	assert.Nil(t, samples[1].UtilPct)

	// without skipSuspended the card is read anyway
	samples, err = newSysfs(Options{}).Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, GpuStateOK, samples[1].State)
}

func TestProbe_SysfsNoCardsIsUnavailable(t *testing.T) {
	old := sysfsRoot
	sysfsRoot = t.TempDir()
	t.Cleanup(func() { sysfsRoot = old })

	_, err := newSysfs(Options{}).Read(context.Background())
	assert.True(t, errors.Is(err, ErrUnavailable))
}

func TestProbe_ListPCIGPUs(t *testing.T) {
	root := fakeSysfs(t)
	addAmdAPU(t, root, "suspended")

	gpus := listPCIGPUs("")
	require.Len(t, gpus, 3)
	assert.Equal(t, "0000:00:02.0", gpus[0].Key)
	assert.True(t, gpus[0].Suspended)
	assert.Equal(t, "0000:03:00.0", gpus[1].Key)
	assert.False(t, gpus[1].Suspended)

	amd := listPCIGPUs(vendorAMD)
	require.Len(t, amd, 1)
	assert.Equal(t, "0000:c3:00.0", amd[0].Key)
}

func TestProbe_SuspendedCLIProbesDoNotRun(t *testing.T) {
	root := fakeSysfs(t)
	addAmdAPU(t, root, "suspended")
	withLookPath(t, true)

	run := &fakeRunner{}
	rocm := newRocmSmi(Options{SkipSuspended: true})
	rocm.run = run.run
	samples, err := rocm.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 1)
	assert.Equal(t, GpuSample{Key: "0000:c3:00.0", Name: "AMD GPU", State: GpuStateSuspended}, samples[0])

	xpu := newXpuSmi(Options{SkipSuspended: true})
	xpu.run = run.run
	samples, err = xpu.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 1)
	assert.Equal(t, "0000:00:02.0", samples[0].Key)

	assert.Empty(t, run.calls, "no CLI may run while one of its GPUs is suspended")
}

func TestProbe_NvidiaSmiQueriesOnlyAwakeGPUs(t *testing.T) {
	root := fakeSysfs(t)
	for addr, status := range map[string]string{"0000:01:00.0": "active", "0000:02:00.0": "suspended"} {
		dir := "devices/pci0000:00/" + addr
		writeFiles(t, root, map[string]string{
			dir + "/class":                "0x030000",
			dir + "/vendor":               "0x10de",
			dir + "/power/runtime_status": status,
		})
		symlink(t, root, "bus/pci/devices/"+addr, "../../../"+dir)
	}
	withLookPath(t, true)
	run := &fakeRunner{out: map[string]string{
		"--query-gpu=": "0, RTX 4090, GPU-a, 00000000:01:00.0, 50, 10, 1000, 24564, 30, 100\n",
	}}
	p := newNvidiaSmi(Options{SkipSuspended: true})
	p.run = run.run

	samples, err := p.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 2)
	assert.Equal(t, GpuStateSuspended, samples[0].State)
	assert.Equal(t, "0000:02:00.0", samples[0].Key)
	assert.Equal(t, "0000:01:00.0", samples[1].Key)
	require.Len(t, run.calls, 1)
	assert.Contains(t, run.calls[0], "--id=00000000:01:00.0")
}
