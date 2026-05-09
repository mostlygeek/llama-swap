package perf

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const mbToBytes = int64(1024 * 1024)

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

	type gpuMetric struct {
		help  string
		name  string
		value func(GpuStat) float64
	}

	metrics := []gpuMetric{
		{"GPU temperature in Celsius", "llamaswap_gpu_temperature_celsius", func(g GpuStat) float64 { return float64(g.TempC) }},
		{"GPU VRAM temperature in Celsius", "llamaswap_gpu_vram_temperature_celsius", func(g GpuStat) float64 { return float64(g.VramTempC) }},
		{"GPU utilization percent (0-100)", "llamaswap_gpu_util_percent", func(g GpuStat) float64 { return g.GpuUtilPct }},
		{"GPU memory utilization percent (0-100)", "llamaswap_gpu_memory_util_percent", func(g GpuStat) float64 { return g.MemUtilPct }},
		{"GPU memory used in bytes", "llamaswap_gpu_memory_used_bytes", func(g GpuStat) float64 { return float64(g.MemUsedMB) * float64(mbToBytes) }},
		{"GPU memory total in bytes", "llamaswap_gpu_memory_total_bytes", func(g GpuStat) float64 { return float64(g.MemTotalMB) * float64(mbToBytes) }},
		{"GPU fan speed percent (0-100)", "llamaswap_gpu_fan_speed_percent", func(g GpuStat) float64 { return g.FanSpeedPct }},
		{"GPU power draw in watts", "llamaswap_gpu_power_draw_watts", func(g GpuStat) float64 { return g.PowerDrawW }},
	}

	for _, m := range metrics {
		fmt.Fprintf(w, "# HELP %s %s\n", m.name, m.help)
		fmt.Fprintf(w, "# TYPE %s gauge\n", m.name)
		for _, g := range gpus {
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
