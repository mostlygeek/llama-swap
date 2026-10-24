package perf

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/perf/probe"
)

// PerformanceReport describes the newest system and GPU readings as text for
// the MCP sys tools. It follows the same rules as /metrics: under poll:
// active it reads on demand while a model is loaded, and says when a section
// was not sampled instead of showing stale values.
func (m *Monitor) PerformanceReport(ctx context.Context) string {
	if !m.Enabled() {
		return "Performance monitoring is disabled (performance.disabled: true).\n"
	}
	sys, gpu := m.Latest(ctx)

	m.mu.Lock()
	conf := m.conf
	now := m.now()
	m.mu.Unlock()

	var b strings.Builder
	switch {
	case conf.System.Disabled:
		b.WriteString("system: disabled (performance.system.disabled: true)\n")
	case sys == nil:
		b.WriteString("system: not sampled (performance.system.poll: active only reads while a model is loaded)\n")
	default:
		fmt.Fprintf(&b, "system (read %s ago):\n", now.Sub(sys.Timestamp).Round(time.Second))
		if n := len(sys.CpuUtilPerCore); n > 0 {
			var sum float64
			for _, v := range sys.CpuUtilPerCore {
				sum += v
			}
			fmt.Fprintf(&b, "  cpu: %.1f%% average over %d cores\n", sum/float64(n), n)
		}
		fmt.Fprintf(&b, "  memory: %s / %s used\n", gib(sys.MemUsedMB), gib(sys.MemTotalMB))
		if sys.SwapTotalMB > 0 {
			fmt.Fprintf(&b, "  swap: %s / %s used\n", gib(sys.SwapUsedMB), gib(sys.SwapTotalMB))
		}
		if sys.LoadAvg1 > 0 || sys.LoadAvg5 > 0 || sys.LoadAvg15 > 0 {
			fmt.Fprintf(&b, "  load average: %.2f %.2f %.2f\n", sys.LoadAvg1, sys.LoadAvg5, sys.LoadAvg15)
		}
	}

	switch {
	case conf.GPU.Disabled:
		b.WriteString("gpus: disabled (performance.gpu.disabled: true)\n")
	case gpu == nil:
		b.WriteString("gpus: not sampled (performance.gpu.poll: active only reads GPUs while a model is loaded)\n")
	case len(gpu.GPUs) == 0:
		b.WriteString("gpus: none found by the configured probes\n")
	default:
		fmt.Fprintf(&b, "gpus (read %s ago):\n", now.Sub(gpu.Timestamp).Round(time.Second))
		for _, g := range gpu.GPUs {
			fmt.Fprintf(&b, "  %s %s: %s\n", g.Key, g.Name, describeGPU(g))
		}
	}
	return b.String()
}

func describeGPU(g probe.GpuSample) string {
	if g.State == probe.GpuStateSuspended {
		return "suspended, not read"
	}
	var parts []string
	if g.UtilPct != nil {
		parts = append(parts, fmt.Sprintf("util %.0f%%", *g.UtilPct))
	}
	if g.VramUsedMB != nil && g.VramTotalMB != nil {
		parts = append(parts, fmt.Sprintf("vram %s / %s", gib(*g.VramUsedMB), gib(*g.VramTotalMB)))
	} else if g.VramUsedMB != nil {
		parts = append(parts, fmt.Sprintf("vram %s used", gib(*g.VramUsedMB)))
	}
	if g.SharedUsedMB != nil && g.SharedTotalMB != nil {
		parts = append(parts, fmt.Sprintf("shared memory %s / %s", gib(*g.SharedUsedMB), gib(*g.SharedTotalMB)))
	}
	if g.TempC != nil {
		parts = append(parts, fmt.Sprintf("temp %.0f C", *g.TempC))
	}
	if g.VramTempC != nil {
		parts = append(parts, fmt.Sprintf("vram temp %.0f C", *g.VramTempC))
	}
	if g.FanPct != nil {
		parts = append(parts, fmt.Sprintf("fan %.0f%%", *g.FanPct))
	}
	if g.PowerW != nil {
		parts = append(parts, fmt.Sprintf("power %.0f W", *g.PowerW))
	}
	if len(parts) == 0 {
		return "no values reported"
	}
	return strings.Join(parts, ", ")
}

func gib(mb int) string {
	return fmt.Sprintf("%.1f GiB", float64(mb)/1024)
}
