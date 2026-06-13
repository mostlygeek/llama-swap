//go:build !windows

package process

// SetupTreeCleanup is a no-op on non-Windows platforms, where upstream process
// teardown is handled via process-group signalling (see runtime_unix.go).
func SetupTreeCleanup() error { return nil }
