package probe

// newPlatformProbe builds the Windows-only probes.
func newPlatformProbe(name string, opts Options) Probe {
	if name == "d3dkmt" {
		return newD3dkmt()
	}
	return nil
}
