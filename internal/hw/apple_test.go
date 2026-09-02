package hw

import "testing"

func TestMetalVersion(t *testing.T) {
	tests := map[string]string{
		"spdisplays_metal4":       "4", // macOS 26 / M4
		"spdisplays_metal3":       "3",
		"spdisplays_metal3family": "3",
		"Metal 3":                 "3",
		"spdisplays_supported":    "", // legacy, no version
		"":                        "",
	}
	for value, want := range tests {
		if got := metalVersion(value); got != want {
			t.Errorf("metalVersion(%q) = %q, want %q", value, got, want)
		}
	}
}
