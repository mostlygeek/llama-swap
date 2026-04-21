//go:build windows

package main

import "os"

// Windows has no SIGHUP — return nil so signal.Notify is a no-op.
func reloadSignals() []os.Signal {
	return nil
}
