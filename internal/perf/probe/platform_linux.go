package probe

// newPlatformProbe builds the Linux-only probes.
func newPlatformProbe(name string, opts Options) Probe {
	switch name {
	case "lact":
		return newLact(opts)
	case "sysfs":
		return newSysfs(opts)
	}
	return nil
}
