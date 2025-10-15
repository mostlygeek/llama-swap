//go:build !windows

package main

import "flag"

func addFlagsIfNeed(flag *flag.FlagSet) {}
func restartIfNeed()                    {}
func runTrayIfAvailable()               {}
