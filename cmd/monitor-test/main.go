package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/perf"
)

func printSysStat(s perf.SysStat) {
	cores := make([]string, len(s.CpuUtilPerCore))
	for i, v := range s.CpuUtilPerCore {
		cores[i] = fmt.Sprintf("%.1f%%", v)
	}
	fmt.Printf("[SYS %s]\n", s.Timestamp.Format("15:04:05"))
	fmt.Printf("  CPU:  %s\n", strings.Join(cores, "  "))
	fmt.Printf("  Mem:  %d MB used / %d MB total (%d MB free)\n", s.MemUsedMB, s.MemTotalMB, s.MemFreeMB)
	fmt.Printf("  Swap: %d MB used / %d MB total\n", s.SwapUsedMB, s.SwapTotalMB)
	fmt.Printf("  Load: %.2f  %.2f  %.2f  (1m 5m 15m)\n", s.LoadAvg1, s.LoadAvg5, s.LoadAvg15)
}

func printGpuStats(gpus []perf.GpuStat) {
	for _, g := range gpus {
		fmt.Printf("[GPU %d %s]\n", g.ID, g.Name)
		fmt.Printf("  Util:  GPU %.1f%%  Mem %.1f%%\n", g.GpuUtilPct, g.MemUtilPct)
		fmt.Printf("  Mem:   %d MB used / %d MB total\n", g.MemUsedMB, g.MemTotalMB)
		fmt.Printf("  Temp:  %d°C   Fan: %.1f%%   Power: %.1f W\n", g.TempC, g.FanSpeedPct, g.PowerDrawW)
	}
}

func main() {
	stream := flag.Bool("stream", false, "stream stats")
	interval := flag.Duration("t", time.Second, "polling interval (clamped to 1s–1h)")
	flag.Parse()

	every := *interval
	if every < time.Second {
		every = time.Second
	} else if every > time.Hour {
		every = time.Hour
	}

	l := logmon.New()
	l.SetLogLevel(logmon.LevelDebug)

	s, err := perf.ReadSysStats()
	if err != nil && err != perf.ErrNotImplemented {
		fmt.Println("Sys Error:", err)
		return
	}
	printSysStat(s)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	gpuCh, err := perf.GetGpuStats(ctx, every, l)
	if err != nil && !errors.Is(err, perf.ErrNotImplemented) && !errors.Is(err, perf.ErrNoGpuTool) {
		fmt.Println("GPU Init Error:", err)
		return
	}

	if gpuCh != nil {
		select {
		case g := <-gpuCh:
			printGpuStats(g)
		case <-ctx.Done():
			fmt.Println("GPU: timed out waiting for stats")
		}
	}

	if *stream {
		m, _ := perf.New(config.PerformanceConfig{Every: every}, l)
		m.Start()
		defer m.Stop()
		sysCh, gpuCh, unsub := m.Subscribe()
		defer unsub()
		for {
			select {
			case s := <-sysCh:
				printSysStat(s)
			case g := <-gpuCh:
				printGpuStats(g)
			}
		}
	}
}
