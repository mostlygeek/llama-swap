//go:build !windows

package tray

func New(stop func(), webpage string) Tray { return nil }
func RestartIfNeed()                       {}
