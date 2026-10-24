package perf

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/perf/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerf_SanitizeLabel(t *testing.T) {
	assert.Equal(t, `a\"b\\c\nd`, sanitizeLabel("a\"b\\c\nd"))
}

func TestPerf_WriteSysMetrics(t *testing.T) {
	var b strings.Builder
	writeSysMetrics(&b, SysStat{
		CpuUtilPerCore: []float64{10.5, 20.0},
		MemTotalMB:     8192,
		MemUsedMB:      4096,
		SwapTotalMB:    2048,
		LoadAvg1:       1.5,
		NetIO:          []NetIOStat{{Name: "eth0", BytesRecv: 1000, BytesSent: 2000}},
	})
	body := b.String()
	assert.Contains(t, body, `llamaswap_cpu_util_percent{core="0"} 10.5`)
	assert.Contains(t, body, "llamaswap_memory_total_bytes 8589934592")
	assert.Contains(t, body, "llamaswap_swap_total_bytes 2147483648")
	assert.Contains(t, body, `llamaswap_load_average{interval="1m"} 1.5`)
	assert.Contains(t, body, `llamaswap_network_bytes_total{interface="eth0",direction="recv"} 1000`)
}

func TestPerf_WriteGpuMetricsSkipsUnreported(t *testing.T) {
	var b strings.Builder
	writeGpuMetrics(&b, []probe.GpuSample{
		{Key: "0000:01:00.0", Name: `RTX "4090"`, State: probe.GpuStateOK, UtilPct: ptr(80.0), VramUsedMB: ptr(1024), PowerW: ptr(300.5)},
		{Key: "0000:c3:00.0", Name: "Radeon 8060S", State: probe.GpuStateOK, SharedUsedMB: ptr(2048), SharedTotalMB: ptr(98304)},
		{Key: "0000:02:00.0", Name: "suspended", State: probe.GpuStateSuspended},
	})
	body := b.String()

	assert.Contains(t, body, `llamaswap_gpu_util_percent{gpu="0000:01:00.0",name="RTX \"4090\""} 80`)
	assert.Contains(t, body, `llamaswap_gpu_memory_used_bytes{gpu="0000:01:00.0",name="RTX \"4090\""} 1.073741824e+09`)
	assert.Contains(t, body, `llamaswap_gpu_power_draw_watts{gpu="0000:01:00.0",name="RTX \"4090\""} 300.5`)
	assert.Contains(t, body, `llamaswap_gpu_shared_memory_used_bytes{gpu="0000:c3:00.0",name="Radeon 8060S"} 2.147483648e+09`)
	assert.Contains(t, body, `llamaswap_gpu_shared_memory_total_bytes{gpu="0000:c3:00.0",name="Radeon 8060S"}`)

	assert.NotContains(t, body, "0000:02:00.0", "a suspended GPU reports nothing")
	assert.NotContains(t, body, "llamaswap_gpu_temperature_celsius", "no GPU reported a temperature")
	assert.NotContains(t, body, `llamaswap_gpu_util_percent{gpu="0000:c3:00.0"`)
	assert.NotContains(t, body, "memory_util_percent")
}

func TestPerf_MetricsHandler(t *testing.T) {
	p := &fakeProbe{name: "nvidia-smi", samples: []probe.GpuSample{sample("0000:01:00.0", "RTX", 42)}}
	m, _ := newTestMonitor(t, nil, p)
	m.Start()
	m.SetModelsLoaded(func() bool { return true })

	rec := httptest.NewRecorder()
	m.MetricsHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil).WithContext(context.Background()))
	require.Equal(t, 200, rec.Code)
	assert.Equal(t, "text/plain; version=0.0.4; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), `llamaswap_gpu_util_percent{gpu="0000:01:00.0",name="RTX"} 42`)
	assert.Equal(t, 1, p.readCount(), "a scrape under poll: active takes a reading")
}
