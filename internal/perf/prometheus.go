package perf

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const mbToBytes = int64(1024 * 1024)

type gpuMetric struct {
	help      string
	name      string
	value     func(GpuStat) float64
	available func(GpuStat) bool
}

// MetricsHandler returns an http.HandlerFunc serving Prometheus text format metrics
// with the most recent system and GPU stats.
func (m *Monitor) MetricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sysStats, gpuStats := m.Current()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		if len(sysStats) > 0 {
			writeSysMetrics(w, sysStats[len(sysStats)-1])
		}

		if len(gpuStats) > 0 {
			writeGpuMetrics(w, latestPerGPU(gpuStats))
		}
	}
}

func writeSysMetrics(w http.ResponseWriter, s SysStat) {
	fmt.Fprintf(w, "# HELP llamaswap_cpu_util_percent CPU utilization per core (0-100)\n")
	fmt.Fprintf(w, "# TYPE llamaswap_cpu_util_percent gauge\n")
	for i, pct := range s.CpuUtilPerCore {
		fmt.Fprintf(w, "llamaswap_cpu_util_percent{core=\"%d\"} %g\n", i, pct)
	}

	fmt.Fprintf(w, "# HELP llamaswap_memory_total_bytes Total memory in bytes\n")
	fmt.Fprintf(w, "# TYPE llamaswap_memory_total_bytes gauge\n")
	fmt.Fprintf(w, "llamaswap_memory_total_bytes %d\n", int64(s.MemTotalMB)*mbToBytes)

	fmt.Fprintf(w, "# HELP llamaswap_memory_used_bytes Used memory in bytes\n")
	fmt.Fprintf(w, "# TYPE llamaswap_memory_used_bytes gauge\n")
	fmt.Fprintf(w, "llamaswap_memory_used_bytes %d\n", int64(s.MemUsedMB)*mbToBytes)

	fmt.Fprintf(w, "# HELP llamaswap_memory_free_bytes Free memory in bytes\n")
	fmt.Fprintf(w, "# TYPE llamaswap_memory_free_bytes gauge\n")
	fmt.Fprintf(w, "llamaswap_memory_free_bytes %d\n", int64(s.MemFreeMB)*mbToBytes)

	fmt.Fprintf(w, "# HELP llamaswap_swap_total_bytes Total swap in bytes\n")
	fmt.Fprintf(w, "# TYPE llamaswap_swap_total_bytes gauge\n")
	fmt.Fprintf(w, "llamaswap_swap_total_bytes %d\n", int64(s.SwapTotalMB)*mbToBytes)

	fmt.Fprintf(w, "# HELP llamaswap_swap_used_bytes Used swap in bytes\n")
	fmt.Fprintf(w, "# TYPE llamaswap_swap_used_bytes gauge\n")
	fmt.Fprintf(w, "llamaswap_swap_used_bytes %d\n", int64(s.SwapUsedMB)*mbToBytes)

	fmt.Fprintf(w, "# HELP llamaswap_load_average Load average\n")
	fmt.Fprintf(w, "# TYPE llamaswap_load_average gauge\n")
	fmt.Fprintf(w, "llamaswap_load_average{interval=\"1m\"} %g\n", s.LoadAvg1)
	fmt.Fprintf(w, "llamaswap_load_average{interval=\"5m\"} %g\n", s.LoadAvg5)
	fmt.Fprintf(w, "llamaswap_load_average{interval=\"15m\"} %g\n", s.LoadAvg15)

	if len(s.NetIO) > 0 {
		fmt.Fprintf(w, "# HELP llamaswap_network_bytes_total Total network bytes transferred\n")
		fmt.Fprintf(w, "# TYPE llamaswap_network_bytes_total counter\n")
		for _, io := range s.NetIO {
			iface := sanitizeLabel(io.Name)
			fmt.Fprintf(w, "llamaswap_network_bytes_total{interface=\"%s\",direction=\"recv\"} %d\n", iface, io.BytesRecv)
			fmt.Fprintf(w, "llamaswap_network_bytes_total{interface=\"%s\",direction=\"sent\"} %d\n", iface, io.BytesSent)
		}
	}
}

func writeGpuMetrics(w http.ResponseWriter, gpus []GpuStat) {
	if len(gpus) == 0 {
		return
	}

	metrics := []gpuMetric{
		{"GPU temperature in Celsius", "llamaswap_gpu_temperature_celsius", func(g GpuStat) float64 { return float64(g.TempC) }, nil},
		{"GPU VRAM temperature in Celsius", "llamaswap_gpu_vram_temperature_celsius", func(g GpuStat) float64 { return float64(g.VramTempC) }, nil},
		{"GPU utilization percent (0-100)", "llamaswap_gpu_util_percent", func(g GpuStat) float64 { return g.GpuUtilPct }, nil},
		{"GPU memory utilization percent (0-100)", "llamaswap_gpu_memory_util_percent", func(g GpuStat) float64 { return g.MemUtilPct }, nil},
		{"GPU memory used in bytes", "llamaswap_gpu_memory_used_bytes", func(g GpuStat) float64 { return float64(g.MemUsedMB) * float64(mbToBytes) }, nil},
		{"GPU memory total in bytes", "llamaswap_gpu_memory_total_bytes", func(g GpuStat) float64 { return float64(g.MemTotalMB) * float64(mbToBytes) }, nil},
		{"GPU fan speed percent (0-100)", "llamaswap_gpu_fan_speed_percent", func(g GpuStat) float64 { return g.FanSpeedPct }, nil},
		{"GPU power draw in watts", "llamaswap_gpu_power_draw_watts", func(g GpuStat) float64 { return g.PowerDrawW }, nil},
		optionalGpuMetric("GPU compute utilization percent (0-100)", "llamaswap_gpu_compute_util_percent", func(g GpuStat) *float64 { return g.ComputeUtilPct }),
		optionalGpuMetric("GPU render utilization percent (0-100)", "llamaswap_gpu_render_util_percent", func(g GpuStat) *float64 { return g.RenderUtilPct }),
		optionalGpuMetric("GPU copy utilization percent (0-100)", "llamaswap_gpu_copy_util_percent", func(g GpuStat) *float64 { return g.CopyUtilPct }),
		optionalGpuMetric("GPU EU active percent (0-100)", "llamaswap_gpu_eu_active_percent", func(g GpuStat) *float64 { return g.EUActivePct }),
		optionalGpuMetric("GPU EU stall percent (0-100)", "llamaswap_gpu_eu_stall_percent", func(g GpuStat) *float64 { return g.EUStallPct }),
		optionalGpuMetric("GPU EU idle percent (0-100)", "llamaswap_gpu_eu_idle_percent", func(g GpuStat) *float64 { return g.EUIdlePct }),
		optionalGpuMetric("GPU memory read bandwidth in bytes per second", "llamaswap_gpu_memory_read_bytes_per_second", func(g GpuStat) *float64 { return g.MemReadBandwidthKBps }, scaleOptionalMetric(1000)),
		optionalGpuMetric("GPU memory write bandwidth in bytes per second", "llamaswap_gpu_memory_write_bytes_per_second", func(g GpuStat) *float64 { return g.MemWriteBandwidthKBps }, scaleOptionalMetric(1000)),
		optionalGpuMetric("GPU memory bandwidth utilization percent (0-100)", "llamaswap_gpu_memory_bandwidth_util_percent", func(g GpuStat) *float64 { return g.MemBandwidthUtilPct }),
		optionalGpuMetric("GPU PCIe receive throughput in bytes per second", "llamaswap_gpu_pcie_rx_bytes_per_second", func(g GpuStat) *float64 { return g.PcieRxMBps }, scaleOptionalMetric(1000*1000)),
		optionalGpuMetric("GPU PCIe transmit throughput in bytes per second", "llamaswap_gpu_pcie_tx_bytes_per_second", func(g GpuStat) *float64 { return g.PcieTxMBps }, scaleOptionalMetric(1000*1000)),
		optionalGpuMetric("GPU graphics clock in MHz", "llamaswap_gpu_graphics_clock_mhz", func(g GpuStat) *float64 { return g.GraphicsClockMHz }),
		optionalGpuMetric("GPU maximum graphics clock in MHz", "llamaswap_gpu_graphics_clock_max_mhz", func(g GpuStat) *float64 { return g.GraphicsClockMaxMHz }),
		optionalGpuMetric("GPU media clock in MHz", "llamaswap_gpu_media_clock_mhz", func(g GpuStat) *float64 { return g.MediaClockMHz }),
		optionalGpuMetric("GPU maximum media clock in MHz", "llamaswap_gpu_media_clock_max_mhz", func(g GpuStat) *float64 { return g.MediaClockMaxMHz }),
		optionalGpuMetric("GPU power limit in watts", "llamaswap_gpu_power_limit_watts", func(g GpuStat) *float64 { return g.PowerLimitW }),
		optionalGpuMetric("GPU energy consumed in joules", "llamaswap_gpu_energy_consumed_joules", func(g GpuStat) *float64 { return g.EnergyConsumedJ }),
	}

	for _, m := range metrics {
		fmt.Fprintf(w, "# HELP %s %s\n", m.name, m.help)
		fmt.Fprintf(w, "# TYPE %s gauge\n", m.name)
		for _, g := range gpus {
			if m.available != nil && !m.available(g) {
				continue
			}
			if g.UUID != "" {
				fmt.Fprintf(w, "%s{id=\"%d\",name=\"%s\",uuid=\"%s\"} %g\n",
					m.name, g.ID, sanitizeLabel(g.Name), sanitizeLabel(g.UUID), m.value(g))
			} else {
				fmt.Fprintf(w, "%s{id=\"%d\",name=\"%s\"} %g\n",
					m.name, g.ID, sanitizeLabel(g.Name), m.value(g))
			}
		}
	}
}

func optionalGpuMetric(help string, name string, value func(GpuStat) *float64, transforms ...func(float64) float64) gpuMetric {
	transform := func(v float64) float64 { return v }
	if len(transforms) > 0 {
		transform = transforms[0]
	}
	return gpuMetric{
		help: help,
		name: name,
		value: func(g GpuStat) float64 {
			return transform(*value(g))
		},
		available: func(g GpuStat) bool {
			return value(g) != nil
		},
	}
}

func scaleOptionalMetric(scale float64) func(float64) float64 {
	return func(value float64) float64 { return value * scale }
}

// latestPerGPU returns the most recent GpuStat for each GPU ID, sorted by ID.
func latestPerGPU(stats []GpuStat) []GpuStat {
	latest := make(map[int]GpuStat)
	for _, g := range stats {
		if prev, ok := latest[g.ID]; !ok || g.Timestamp.After(prev.Timestamp) {
			latest[g.ID] = g
		}
	}
	result := make([]GpuStat, 0, len(latest))
	for _, g := range latest {
		result = append(result, g)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// sanitizeLabel escapes characters that are invalid in Prometheus label values.
func sanitizeLabel(s string) string {
	return strings.NewReplacer(`"`, `\"`, `\`, `\\`, "\n", `\n`).Replace(s)
}
