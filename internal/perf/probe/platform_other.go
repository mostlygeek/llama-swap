//go:build !linux && !windows && !darwin

package probe

// newPlatformProbe builds OS-specific probes. There are none on this OS.
func newPlatformProbe(name string, opts Options) Probe { return nil }
