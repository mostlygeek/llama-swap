package perf

import (
	"strconv"
	"strings"
	"time"
)

// ParseNvidiaSmiLine parses a single line from nvidia-smi CSV output.
// Format: index,name,uuid,temperature.gpu,utilization.gpu,memory.used,memory.total,fan.speed,power.draw
func ParseNvidiaSmiLine(line string) *GpuStat {
	fields := strings.Split(line, ",")
	if len(fields) < 9 {
		return nil
	}

	id, _ := strconv.Atoi(strings.TrimSpace(fields[0]))
	name := strings.TrimSpace(fields[1])
	uuid := strings.TrimSpace(fields[2])
	tempC, _ := strconv.Atoi(strings.TrimSpace(fields[3]))
	gpuUtil, _ := strconv.ParseFloat(strings.TrimSpace(fields[4]), 64)
	memUsed, _ := strconv.Atoi(strings.TrimSpace(fields[5]))
	memTotal, _ := strconv.Atoi(strings.TrimSpace(fields[6]))
	fanSpeed, _ := strconv.ParseFloat(strings.TrimSpace(fields[7]), 64)
	powerDraw, _ := strconv.ParseFloat(strings.TrimSpace(fields[8]), 64)

	var memUtil float64
	if memTotal > 0 {
		memUtil = float64(memUsed) / float64(memTotal) * 100
	}

	return &GpuStat{
		Timestamp:   time.Now(),
		ID:          id,
		Name:        name,
		UUID:        uuid,
		TempC:       tempC,
		GpuUtilPct:  gpuUtil,
		MemUtilPct:  memUtil,
		MemUsedMB:   memUsed,
		MemTotalMB:  memTotal,
		FanSpeedPct: fanSpeed,
		PowerDrawW:  powerDraw,
	}
}
