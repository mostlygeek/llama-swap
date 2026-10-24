package perf

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/perf/probe"
)

const mbToBytes = int64(1024 * 1024)

// MetricsHandler serves the newest system and GPU readings in Prometheus text
// format. A scrape counts as a viewer: under poll: active it takes a fresh
// reading when a model is loaded. GPU gauges are left out when the GPUs
// were not sampled (no model loaded), and so is any field a probe did not
// report.
func (m *Monitor) MetricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sys, gpu := m.Latest(r.Context())
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if sys != nil {
			writeSysMetrics(w, *sys)
		}
		if gpu != nil {
			writeGpuMetrics(w, gpu.GPUs)
		}
	}
}

func writeSysMetrics(w io.Writer, s SysStat) {
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

func writeGpuMetrics(w io.Writer, gpus []probe.GpuSample) {
	mb := func(v *int) *float64 {
		if v == nil {
			return nil
		}
		b := float64(int64(*v) * mbToBytes)
		return &b
	}
	metrics := []struct {
		help  string
		name  string
		value func(probe.GpuSample) *float64
	}{
		{"GPU temperature in Celsius", "llamaswap_gpu_temperature_celsius", func(g probe.GpuSample) *float64 { return g.TempC }},
		{"GPU VRAM temperature in Celsius", "llamaswap_gpu_vram_temperature_celsius", func(g probe.GpuSample) *float64 { return g.VramTempC }},
		{"GPU utilization percent (0-100)", "llamaswap_gpu_util_percent", func(g probe.GpuSample) *float64 { return g.UtilPct }},
		{"GPU dedicated memory used in bytes", "llamaswap_gpu_memory_used_bytes", func(g probe.GpuSample) *float64 { return mb(g.VramUsedMB) }},
		{"GPU dedicated memory total in bytes", "llamaswap_gpu_memory_total_bytes", func(g probe.GpuSample) *float64 { return mb(g.VramTotalMB) }},
		{"GPU shared memory used in bytes (GTT or unified memory)", "llamaswap_gpu_shared_memory_used_bytes", func(g probe.GpuSample) *float64 { return mb(g.SharedUsedMB) }},
		{"GPU shared memory total in bytes (GTT or unified memory)", "llamaswap_gpu_shared_memory_total_bytes", func(g probe.GpuSample) *float64 { return mb(g.SharedTotalMB) }},
		{"GPU fan speed percent (0-100)", "llamaswap_gpu_fan_speed_percent", func(g probe.GpuSample) *float64 { return g.FanPct }},
		{"GPU power draw in watts", "llamaswap_gpu_power_draw_watts", func(g probe.GpuSample) *float64 { return g.PowerW }},
	}

	for _, m := range metrics {
		headerDone := false
		for _, g := range gpus {
			v := m.value(g)
			if v == nil {
				continue
			}
			if !headerDone {
				fmt.Fprintf(w, "# HELP %s %s\n", m.name, m.help)
				fmt.Fprintf(w, "# TYPE %s gauge\n", m.name)
				headerDone = true
			}
			fmt.Fprintf(w, "%s{gpu=\"%s\",name=\"%s\"} %g\n", m.name, sanitizeLabel(g.Key), sanitizeLabel(g.Name), *v)
		}
	}
}

// sanitizeLabel escapes characters that are invalid in Prometheus label values.
func sanitizeLabel(s string) string {
	return strings.NewReplacer(`"`, `\"`, `\`, `\\`, "\n", `\n`).Replace(s)
}
