//go:build unix && !darwin

package perf

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Fixture mirrors a real Intel Arc Pro B70 (xe driver) as observed on kernel
// 7.1: hwmon temps labelled pkg/vram, energy counter, fan RPM+max, a 32 GiB
// VRAM BAR in the PCI resource file, and fdinfo using the cycles-based engine
// format with KiB-suffixed memory.
const xeFdinfo = `pos:	0
flags:	02000002
drm-driver:	xe
drm-client-id:	20
drm-pdev:	0000:03:00.0
drm-total-system:	0
drm-total-gtt:	2 MiB
drm-resident-gtt:	2 MiB
drm-total-vram0:	16946852 KiB
drm-resident-vram0:	16000 MiB
drm-cycles-rcs:	744
drm-total-cycles-rcs:	18956600119
drm-cycles-bcs:	497250
drm-total-cycles-bcs:	18956600119
drm-cycles-ccs:	386293167
drm-total-cycles-ccs:	18956600119
drm-engine-capacity-ccs:	2
`

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func symlink(t *testing.T, root, link, target string) {
	t.Helper()
	path := filepath.Join(root, link)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

// fakeSysfs builds a sysfs tree with one xe dGPU (with hwmon) and one bare
// i915 iGPU (no hwmon; must be skipped by discovery).
func fakeSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	gpuDir := "devices/pci0000:00/0000:00:01.0/0000:01:00.0/0000:03:00.0"
	igpuDir := "devices/pci0000:00/0000:00:02.0"

	writeFiles(t, root, map[string]string{
		gpuDir + "/vendor":                     "0x8086",
		gpuDir + "/device":                     "0xe223",
		gpuDir + "/resource":                   "0x00000041f8000000 0x00000041f8ffffff 0x000000000014220c\n0x0000000000000000 0x0000000000000000 0x0000000000000000\n0x0000004800000000 0x0000004fffffffff 0x000000000014220c\n",
		gpuDir + "/hwmon/hwmon9/name":          "xe",
		gpuDir + "/hwmon/hwmon9/temp2_input":   "48000",
		gpuDir + "/hwmon/hwmon9/temp2_label":   "pkg",
		gpuDir + "/hwmon/hwmon9/temp3_input":   "50000",
		gpuDir + "/hwmon/hwmon9/temp3_label":   "vram",
		gpuDir + "/hwmon/hwmon9/temp4_input":   "39000",
		gpuDir + "/hwmon/hwmon9/temp4_label":   "mctrl",
		gpuDir + "/hwmon/hwmon9/fan1_input":    "1074",
		gpuDir + "/hwmon/hwmon9/fan1_max":      "4980",
		gpuDir + "/hwmon/hwmon9/energy1_input": "255045692199",
		gpuDir + "/hwmon/hwmon9/energy1_label": "card",
		igpuDir + "/vendor":                    "0x8086",
		igpuDir + "/device":                    "0x4692",
	})

	// driver symlinks (targets must exist for EvalSymlinks)
	for _, d := range []string{"xe", "i915"} {
		if err := os.MkdirAll(filepath.Join(root, "bus/pci/drivers", d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	symlink(t, root, gpuDir+"/driver", "../../../../../bus/pci/drivers/xe")
	symlink(t, root, igpuDir+"/driver", "../../../bus/pci/drivers/i915")

	// drm class entries
	symlink(t, root, "class/drm/card0/device", "../../../"+gpuDir)
	symlink(t, root, "class/drm/card1/device", "../../../"+igpuDir)
	symlink(t, root, "bus/pci/devices/0000:03:00.0", "../../../"+gpuDir)

	return root
}

func withSysfs(t *testing.T, root string) {
	t.Helper()
	old := sysfsRoot
	sysfsRoot = root
	t.Cleanup(func() { sysfsRoot = old })
}

func TestDiscoverSysfsGpusSkipsIGpuWithoutHwmon(t *testing.T) {
	withSysfs(t, fakeSysfs(t))

	gpus := discoverSysfsGpus()
	if len(gpus) != 1 {
		t.Fatalf("expected 1 GPU (xe with hwmon), got %d", len(gpus))
	}
	g := gpus[0]
	if g.driver != "xe" {
		t.Errorf("driver = %q, want xe", g.driver)
	}
	if g.uuid != "0000:03:00.0" {
		t.Errorf("uuid = %q, want 0000:03:00.0", g.uuid)
	}
	if g.vramTotalMB != 32*1024 {
		t.Errorf("vramTotalMB = %d, want %d", g.vramTotalMB, 32*1024)
	}
	if want := "Intel xe 0xe223"; g.name != want {
		t.Errorf("name = %q, want %q", g.name, want)
	}
}

func TestReadHwmonTempsFanWhenActive(t *testing.T) {
	sysRoot := fakeSysfs(t)
	withSysfs(t, sysRoot)

	procRootLocal := t.TempDir()
	writeFiles(t, procRootLocal, map[string]string{"1234/fdinfo/17": xeFdinfo})
	symlink(t, procRootLocal, "1234/fd/17", "/dev/dri/renderD128")
	oldProc := procRoot
	procRoot = procRootLocal
	t.Cleanup(func() { procRoot = oldProc })

	g := discoverSysfsGpus()[0]
	stat, err := g.poll()
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if stat.TempC != 48 {
		t.Errorf("TempC = %d, want 48 (pkg label)", stat.TempC)
	}
	if stat.VramTempC != 50 {
		t.Errorf("VramTempC = %d, want 50 (vram label)", stat.VramTempC)
	}
	if want := 1074.0 / 4980.0 * 100; stat.FanSpeedPct < want-0.01 || stat.FanSpeedPct > want+0.01 {
		t.Errorf("FanSpeedPct = %.2f, want ~%.2f", stat.FanSpeedPct, want)
	}
	if stat.MemTotalMB != 32*1024 {
		t.Errorf("MemTotalMB = %d, want %d (BAR-derived)", stat.MemTotalMB, 32*1024)
	}
}

// An idle GPU (no process holds the render node) must not be woken just to
// read sensors: telemetry zeroes out instead of touching hwmon.
func TestIdleGpuTelemetryZeroedWithoutHwmon(t *testing.T) {
	withSysfs(t, fakeSysfs(t))
	oldProc := procRoot
	procRoot = t.TempDir()
	t.Cleanup(func() { procRoot = oldProc })

	g := discoverSysfsGpus()[0]
	stat, err := g.poll()
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if stat.TempC != 0 || stat.FanSpeedPct != 0 || stat.PowerDrawW != 0 {
		t.Errorf("idle stat should be zeroed, got %+v", stat)
	}
	if stat.MemTotalMB != 32*1024 {
		t.Errorf("MemTotalMB = %d, want %d (BAR read does not wake the GPU)", stat.MemTotalMB, 32*1024)
	}
}

func TestPollReportsVramViaFdinfo(t *testing.T) {
	sysRoot := fakeSysfs(t)

	procRootLocal := t.TempDir()
	writeFiles(t, procRootLocal, map[string]string{
		"1234/fdinfo/17": xeFdinfo,
	})
	symlink(t, procRootLocal, "1234/fd/17", "/dev/dri/renderD128")

	oldSys, oldProc := sysfsRoot, procRoot
	sysfsRoot, procRoot = sysRoot, procRootLocal
	t.Cleanup(func() { sysfsRoot, procRoot = oldSys, oldProc })

	g := discoverSysfsGpus()[0]
	stat, err := g.poll()
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if want := 16946852 / 1024; stat.MemUsedMB != want {
		t.Errorf("MemUsedMB = %d, want %d (fdinfo drm-total-vram0)", stat.MemUsedMB, want)
	}
	if stat.MemUtilPct < 50.4 || stat.MemUtilPct > 50.6 {
		t.Errorf("MemUtilPct = %.2f, want ~50.4", stat.MemUtilPct)
	}
}

// Even while active, hwmon must not be read more than once per
// sysfsHwmonMinInterval -- each read wakes a sleeping card.
func TestHwmonThrottledWhileActive(t *testing.T) {
	sysRoot := fakeSysfs(t)
	withSysfs(t, sysRoot)

	procRootLocal := t.TempDir()
	writeFiles(t, procRootLocal, map[string]string{"1234/fdinfo/17": xeFdinfo})
	symlink(t, procRootLocal, "1234/fd/17", "/dev/dri/renderD128")
	oldProc := procRoot
	procRoot = procRootLocal
	t.Cleanup(func() { procRoot = oldProc })

	g := discoverSysfsGpus()[0]
	first, err := g.poll()
	if err != nil || first.TempC != 48 {
		t.Fatalf("first poll should read hwmon (TempC=48), got %+v err=%v", first, err)
	}
	second, _ := g.poll()
	if second.TempC != 0 {
		t.Errorf("immediate second poll must skip hwmon, got TempC=%d", second.TempC)
	}
	g.lastHwmonAt = time.Now().Add(-2 * sysfsHwmonMinInterval)
	third, _ := g.poll()
	if third.TempC != 48 {
		t.Errorf("poll after interval must read hwmon again, got TempC=%d", third.TempC)
	}
}

func TestFdInfoVramFallsBackToResident(t *testing.T) {
	kv := map[string]string{
		"drm-resident-vram0": "16000 MiB",
	}
	if got, want := fdInfoVramKB(kv)/1024, uint64(16000); got != want {
		t.Errorf("resident fallback = %d MiB, want %d", got, want)
	}
}

func TestParseSizeKB(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
	}{
		{"16946852 KiB", 16946852},
		{"16000 MiB", 16000 * 1024},
		{"2 GiB", 2 * 1024 * 1024},
		{"1048576", 1024}, // bare bytes
		{"0", 0},
	}
	for _, c := range cases {
		if got, ok := parseSizeKB(c.in); !ok || got != c.want {
			t.Errorf("parseSizeKB(%q) = %d,%v; want %d,true", c.in, got, ok, c.want)
		}
	}
}

func TestFdInfoEngineUtilCycles(t *testing.T) {
	state := map[string]map[string]fdEngineSample{}
	out := map[string]float64{}

	fdInfoEnginePct(map[string]string{
		"drm-client-id": "20", "drm-cycles-ccs": "1000", "drm-total-cycles-ccs": "1000000",
	}, state, 1.0, out) // seeds state

	out = map[string]float64{}
	fdInfoEnginePct(map[string]string{
		"drm-client-id": "20", "drm-cycles-ccs": "351000", "drm-total-cycles-ccs": "2010000",
	}, state, 1.0, out)

	if got := out["ccs"]; got < 34.6 || got > 34.7 {
		t.Errorf("ccs util = %.2f%%, want ~34.65%% (350000/1010000)", got)
	}
}

func TestFdInfoEngineUtilNs(t *testing.T) {
	state := map[string]map[string]fdEngineSample{}
	out := map[string]float64{}

	fdInfoEnginePct(map[string]string{
		"drm-client-id": "7", "drm-engine-render": "0 ns",
	}, state, 1.0, out)

	out = map[string]float64{}
	fdInfoEnginePct(map[string]string{
		"drm-client-id": "7", "drm-engine-render": "500000000 ns",
	}, state, 1.0, out)

	if got := out["render"]; got < 49.9 || got > 50.1 {
		t.Errorf("render util = %.2f%%, want ~50%%", got)
	}
}
