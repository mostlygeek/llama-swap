//go:build linux

package hw

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHardware_LinuxCgroupMemoryLimit(t *testing.T) {
	dir := t.TempDir()
	limited := filepath.Join(dir, "memory.max")
	unlimited := filepath.Join(dir, "unlimited")
	if err := os.WriteFile(limited, []byte("2147483648\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unlimited, []byte("max\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := limitedMemoryCapacity(8*1024*1024*1024, []string{unlimited, limited}); got != 2*1024*1024*1024 {
		t.Fatalf("limitedMemoryCapacity() = %d", got)
	}
}

func TestHardware_LinuxZoneinfoMemoryCapacity(t *testing.T) {
	dir := t.TempDir()
	zoneinfo := `Node 0, zone DMA
        spanned  524288
        present  393216
        managed  390000
Node 0, zone Normal
        spanned  33554432
        present  33161216
        managed  32000000
Node 0, zone Device
        spanned  524288
        present  524288
        managed  0
`
	path := filepath.Join(dir, "zoneinfo")
	if err := os.WriteFile(path, []byte(zoneinfo), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := zoneinfoMemoryCapacity(path, 4096); got != 128*1024*1024*1024 {
		t.Fatalf("zoneinfoMemoryCapacity() = %d", got)
	}
}

func TestHardware_LinuxKFDArchitecture(t *testing.T) {
	root := t.TempDir()
	nodesRoot := filepath.Join(root, "kfd", "nodes")
	drmRoot := filepath.Join(root, "drm")
	deviceRoot := filepath.Join(root, "dev", "dri")
	pciDevice := filepath.Join(root, "pci", "0000:65:00.0")
	for _, path := range []string{
		filepath.Join(nodesRoot, "1"),
		filepath.Join(drmRoot, "renderD128"),
		deviceRoot,
		pciDevice,
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	properties := "gfx_target_version 110501\ndrm_render_minor 128\nvendor_id 4098\n"
	if err := os.WriteFile(filepath.Join(nodesRoot, "1", "properties"), []byte(properties), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pciDevice, "vendor"), []byte("0x1002\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deviceRoot, "renderD128"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pciDevice, filepath.Join(drmRoot, "renderD128", "device")); err != nil {
		t.Fatal(err)
	}

	got, err := detectAMDKFD(nodesRoot, drmRoot, deviceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].identity != "0000:65:00.0" || stringValue(got[0].value.Architecture) != "gfx1151" {
		t.Fatalf("detectAMDKFD() = %+v", got)
	}
}

func TestHardware_LinuxGFXTargetFormatting(t *testing.T) {
	tests := map[uint64]string{
		0:      "",
		90010:  "gfx90a",
		110000: "gfx1100",
		110501: "gfx1151",
	}
	for version, want := range tests {
		if got := formatGFXTarget(version); got != want {
			t.Errorf("formatGFXTarget(%d) = %q, want %q", version, got, want)
		}
	}
}

func TestHardware_ParseROCmCSV(t *testing.T) {
	output := "device,Device Name,GUID,VRAM Total Memory (B),Card Series,GFX Version,Driver version,PCI Bus,Max Graphics Package Power (W)\n" +
		"card0,AMD Radeon,abc,25769803776,Radeon RX 7900 XTX,gfx1100,6.12.12,0000:03:00.0,339\n"
	got, err := parseROCmCSV(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].identity != "0000:03:00.0" || stringValue(got[0].value.Model) != "Radeon RX 7900 XTX" {
		t.Fatalf("parseROCmCSV() = %+v", got)
	}
}
