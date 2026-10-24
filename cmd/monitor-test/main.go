// monitor-test prints what llama-swap's performance monitor sees on this
// host: system stats, the probes auto picks, and each probe's GPU readings.
package main

import (
	"context"
	"flag"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/hw"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/perf/probe"
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

func printGpuSamples(source string, samples []probe.GpuSample) {
	for _, g := range samples {
		fmt.Printf("[GPU %s %s via %s] %s\n", g.Key, g.Name, source, g.State)
		fmt.Printf("  Util: %s%%  Temp: %s°C  VRAM temp: %s°C  Fan: %s%%  Power: %s W\n",
			f(g.UtilPct), f(g.TempC), f(g.VramTempC), f(g.FanPct), f(g.PowerW))
		fmt.Printf("  VRAM: %s / %s MB   Shared: %s / %s MB\n",
			i(g.VramUsedMB), i(g.VramTotalMB), i(g.SharedUsedMB), i(g.SharedTotalMB))
	}
}

func f(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f", *v)
}

func i(v *int) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(*v)
}

func main() {
	stream := flag.Bool("stream", false, "keep reading through the monitor, as llama-swap does")
	interval := flag.Duration("t", 5*time.Second, "polling interval for -stream (minimum 5s)")
	probes := flag.String("probes", "", "comma separated probes to read (default: every probe for this OS)")
	skipSuspended := flag.Bool("skip-suspended", true, "do not read runtime-suspended GPUs (Linux)")
	flag.Parse()

	l := logmon.New()
	l.SetLogLevel(logmon.LevelDebug)

	s, err := perf.ReadSysStats()
	if err != nil {
		fmt.Println("Sys Error:", err)
		return
	}
	printSysStat(s)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	snapshot, err := hw.Detect(ctx, "monitor-test")
	cancel()
	if err != nil {
		fmt.Println("Hardware detection error:", err)
	}
	conf := config.DefaultPerformanceConfig()
	m, _ := perf.New(conf, l, &snapshot)
	fmt.Printf("auto picks: %s\n", strings.Join(m.ProbeNames(), ", "))

	names := perf.AllProbes(runtime.GOOS)
	if *probes != "" {
		names = strings.Split(*probes, ",")
		for i := range names {
			names[i] = strings.TrimSpace(names[i])
		}
	}
	for _, name := range names {
		p, err := probe.New(name, probe.Options{SkipSuspended: *skipSuspended})
		if err != nil {
			fmt.Printf("[%s] %s\n", name, err)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		samples, err := p.Read(ctx)
		cancel()
		if err != nil {
			fmt.Printf("[%s] %s\n", name, err)
			continue
		}
		printGpuSamples(name, samples)
	}

	if !*stream {
		return
	}
	conf.System.Every = max(*interval, 5*time.Second)
	conf.GPU.Every = conf.System.Every
	conf.GPU.Poll = config.PerformancePollAlways
	conf.GPU.SkipSuspended = *skipSuspended
	if *probes != "" {
		conf.GPU.Probes = names
	}
	m.UpdateConfig(conf)
	m.Start()
	defer m.Stop()
	_, events, stop := m.Watch()
	defer stop()
	for e := range events {
		switch e.Kind {
		case perf.EventSys:
			printSysStat(*e.Sys)
		case perf.EventGPU:
			fmt.Printf("[GPU reading %s]\n", e.GPU.Timestamp.Format("15:04:05"))
			printGpuSamples("monitor", e.GPU.GPUs)
		}
	}
}
