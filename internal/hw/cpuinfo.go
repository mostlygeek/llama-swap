//go:build !darwin

package hw

import (
	"context"

	"github.com/shirou/gopsutil/v4/cpu"
)

func cpuInfo(ctx context.Context) ([]cpu.InfoStat, error) {
	return cpu.InfoWithContext(ctx)
}
