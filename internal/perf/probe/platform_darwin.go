package probe

// newPlatformProbe builds the macOS-only probes. It also builds mactop here so
// mactop gets its GPU memory from ioreg, since mactop only reports
// whole-system memory.
func newPlatformProbe(name string, opts Options) Probe {
	switch name {
	case "mactop":
		p := newMactop(opts)
		p.gpuMemUsedMB = newIoreg(Options{}).gpuMemUsedMB
		return p
	case "ioreg":
		return newIoreg(opts)
	}
	return nil
}
