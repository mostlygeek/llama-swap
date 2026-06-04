package perf

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

func getGpuStats(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	if ch, err := tryMactop(ctx, every, logger); err == nil {
		logger.Info("using mactop for GPU monitoring")
		return ch, nil
	} else {
		logger.Debugf("mactop: %s", err.Error())
	}

	if ch, err := tryIoreg(ctx, every, logger); err == nil {
		logger.Info("using ioreg for GPU monitoring")
		return ch, nil
	} else {
		logger.Debugf("ioreg: %s", err.Error())
	}

	return nil, ErrNoGpuTool
}

// tryIoreg polls `ioreg -r -c IOGPU -d 1 -f` for Apple Silicon GPU stats. It is
// a fallback for when mactop is not installed. ioreg exposes GPU utilization and
// used memory but not power, temperature, or fan speed.
func tryIoreg(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	if _, err := exec.LookPath("ioreg"); err != nil {
		return nil, ErrNoGpuTool
	}

	// Verify ioreg actually reports a GPU device before committing to it, so we
	// can fall through to ErrNoGpuTool otherwise.
	if stat := sampleIoreg(ctx); stat == nil {
		return nil, fmt.Errorf("ioreg reported no GPU device")
	}

	if every < time.Second {
		every = time.Second
	}

	ch := make(chan []GpuStat, 1)

	go func() {
		defer close(ch)
		ticker := time.NewTicker(every)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stat := sampleIoreg(ctx)
				if stat == nil {
					continue
				}
				select {
				case ch <- []GpuStat{*stat}:
				default:
				}
			}
		}
	}()

	return ch, nil
}

// sampleIoreg runs ioreg once and parses a single GpuStat, or returns nil.
func sampleIoreg(ctx context.Context) *GpuStat {
	out, err := exec.CommandContext(ctx, "ioreg", "-r", "-c", "IOGPU", "-d", "1", "-f").Output()
	if err != nil {
		return nil
	}

	var memTotalMB int
	if vmStat, err := mem.VirtualMemory(); err == nil {
		memTotalMB = int(vmStat.Total / (1024 * 1024))
	}

	return ParseIoregOutput(out, memTotalMB)
}

// overlayIoregMem replaces a GpuStat's memory fields with the GPU-attributed
// unified memory reported by ioreg. mactop only exposes whole-system memory, so
// without this the mactop and ioreg backends would report different memory
// semantics. It is a no-op when ioreg is unavailable or reports no GPU memory,
// leaving the mactop-supplied values in place.
func overlayIoregMem(ctx context.Context, stat *GpuStat) {
	ioStat := sampleIoreg(ctx)
	if ioStat == nil {
		return
	}
	stat.MemUsedMB = ioStat.MemUsedMB
	stat.MemTotalMB = ioStat.MemTotalMB
	stat.MemUtilPct = ioStat.MemUtilPct
}

// tryMactop streams Apple Silicon GPU stats from mactop's headless mode.
// See https://github.com/metaspartan/mactop. mactop emits one JSON object per
// sample to stdout, which we parse into GpuStat.
func tryMactop(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	if _, err := exec.LookPath("mactop"); err != nil {
		return nil, ErrNoGpuTool
	}

	// mactop samples power over the interval, so give it at least a second.
	intervalMs := int(every.Milliseconds())
	if intervalMs < 1000 {
		intervalMs = 1000
	}

	cmd := exec.CommandContext(ctx, "mactop",
		"--headless",
		"--format", "json",
		"--interval", fmt.Sprintf("%d", intervalMs),
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mactop stdout pipe failed: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mactop start failed: %w", err)
	}

	ch := make(chan []GpuStat, 1)

	go func() {
		defer close(ch)

		scanner := bufio.NewScanner(stdout)
		// mactop's JSON objects can be large; allow generous line lengths.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			stat := ParseMactopLine(line)
			if stat != nil {
				// mactop only reports whole-system memory; overlay ioreg's
				// GPU-attributed unified memory so both backends are consistent.
				overlayIoregMem(ctx, stat)
				select {
				case ch <- []GpuStat{*stat}:
				default:
				}
			}
		}
		cmd.Wait()
	}()

	return ch, nil
}

func readSysStats() (SysStat, error) {
	cpuPcts, err := cpu.Percent(0, true)
	if err != nil {
		return SysStat{}, err
	}

	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return SysStat{}, err
	}

	const toMB = 1024 * 1024

	var swapTotalMB, swapUsedMB int
	if swapStat, err := mem.SwapMemory(); err == nil {
		swapTotalMB = int(swapStat.Total / toMB)
		swapUsedMB = int(swapStat.Used / toMB)
	}

	var loadAvg1, loadAvg5, loadAvg15 float64
	if loadStat, err := load.Avg(); err == nil {
		loadAvg1 = loadStat.Load1
		loadAvg5 = loadStat.Load5
		loadAvg15 = loadStat.Load15
	}

	return SysStat{
		Timestamp:      time.Now(),
		CpuUtilPerCore: cpuPcts,
		MemTotalMB:     int(vmStat.Total / toMB),
		MemUsedMB:      int(vmStat.Used / toMB),
		MemFreeMB:      int(vmStat.Free / toMB),
		SwapTotalMB:    swapTotalMB,
		SwapUsedMB:     swapUsedMB,
		LoadAvg1:       loadAvg1,
		LoadAvg5:       loadAvg5,
		LoadAvg15:      loadAvg15,
	}, nil
}
