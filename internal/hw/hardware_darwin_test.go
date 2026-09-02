//go:build darwin

package hw

import "testing"

func TestHardware_DarwinSystemProfilerAppleSilicon(t *testing.T) {
	snapshot := validTestSnapshot()
	snapshot.OperatingSystem.Family = "macos"
	snapshot.Memory.CapacityBytes = 64 * 1024 * 1024 * 1024
	data := []byte(`{
  "SPHardwareDataType": [{"chip_type":"Apple M4 Max"}],
  "SPDisplaysDataType": [{
    "_name":"Apple M4 Max",
    "spdisplays_vendor":"sppci_vendor_Apple",
    "spdisplays_metal":"spdisplays_supported"
  }]
}`)
	got, err := parseSystemProfiler(data, &snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if stringValue(snapshot.CPU.Model) != "Apple M4 Max" || stringValue(snapshot.CPU.Vendor) != "Apple" {
		t.Fatalf("CPU = %+v", snapshot.CPU)
	}
	if len(got) != 1 || got[0].value.Memory.Kind != "unified" || *got[0].value.Memory.CapacityBytes != snapshot.Memory.CapacityBytes {
		t.Fatalf("accelerators = %+v", got)
	}
}

func TestHardware_DarwinProfilerMemory(t *testing.T) {
	if got := parseProfilerMemory("24 GB"); got != 24*1024*1024*1024 {
		t.Fatalf("parseProfilerMemory() = %d", got)
	}
}
