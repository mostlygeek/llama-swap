//go:build !linux && !darwin && !windows

package hw

import (
	"context"

	"github.com/shirou/gopsutil/v4/host"
)

func detectPlatform(context.Context, *HardwareSnapshot) ([]detectedAccelerator, error) {
	return nil, nil
}

func detectEnvironment(context.Context, *host.InfoStat) ExecutionEnvironment {
	return ExecutionEnvironment{Kind: "unknown"}
}

func platformMemoryCapacity(total uint64) uint64 { return total }
