package perf

import (
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
)

// ReadSysStats reads CPU, memory, swap, load average and network counters
// through gopsutil. Fields a platform does not support stay 0, for example
// load average on Windows.
func ReadSysStats() (SysStat, error) {
	cpuPcts, err := cpu.Percent(0, true)
	if err != nil {
		return SysStat{}, err
	}
	vm, err := mem.VirtualMemory()
	if err != nil {
		return SysStat{}, err
	}

	const toMB = 1024 * 1024
	s := SysStat{
		Timestamp:      time.Now(),
		CpuUtilPerCore: cpuPcts,
		MemTotalMB:     int(vm.Total / toMB),
		MemUsedMB:      int(vm.Used / toMB),
		MemFreeMB:      int(vm.Free / toMB),
		NetIO:          []NetIOStat{},
	}
	if swap, err := mem.SwapMemory(); err == nil {
		s.SwapTotalMB = int(swap.Total / toMB)
		s.SwapUsedMB = int(swap.Used / toMB)
	}
	if avg, err := load.Avg(); err == nil {
		s.LoadAvg1, s.LoadAvg5, s.LoadAvg15 = avg.Load1, avg.Load5, avg.Load15
	}
	if counters, err := psnet.IOCounters(true); err == nil {
		for _, c := range counters {
			if c.Name == "lo" {
				continue
			}
			s.NetIO = append(s.NetIO, NetIOStat{Name: c.Name, BytesRecv: c.BytesRecv, BytesSent: c.BytesSent})
		}
	}
	return s, nil
}
