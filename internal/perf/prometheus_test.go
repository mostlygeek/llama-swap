package perf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal", "normal"},
		{"", ""},
		{`with"quote`, `with\"quote`},
		{`with\backslash`, `with\\backslash`},
		{"with\nnewline", `with\nnewline`},
		{`"both\n"`, `\"both\\n\"`},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, sanitizeLabel(tc.input), "input: %q", tc.input)
	}
}

func TestLatestPerGPU_Empty(t *testing.T) {
	result := latestPerGPU(nil)
	assert.Empty(t, result)
}

func TestLatestPerGPU_Single(t *testing.T) {
	now := time.Now()
	stats := []GpuStat{{ID: 0, Name: "gpu0", Timestamp: now}}
	result := latestPerGPU(stats)
	require.Len(t, result, 1)
	assert.Equal(t, "gpu0", result[0].Name)
}

func TestLatestPerGPU_PicksLatest(t *testing.T) {
	earlier := time.Now().Add(-time.Second)
	later := time.Now()
	stats := []GpuStat{
		{ID: 0, Name: "old", TempC: 50, Timestamp: earlier},
		{ID: 0, Name: "new", TempC: 70, Timestamp: later},
	}
	result := latestPerGPU(stats)
	require.Len(t, result, 1)
	assert.Equal(t, "new", result[0].Name)
	assert.Equal(t, 70, result[0].TempC)
}

func TestLatestPerGPU_MultipleGPUsSortedByID(t *testing.T) {
	now := time.Now()
	stats := []GpuStat{
		{ID: 2, Name: "gpu2", Timestamp: now},
		{ID: 0, Name: "gpu0", Timestamp: now},
		{ID: 1, Name: "gpu1", Timestamp: now},
	}
	result := latestPerGPU(stats)
	require.Len(t, result, 3)
	assert.Equal(t, 0, result[0].ID)
	assert.Equal(t, 1, result[1].ID)
	assert.Equal(t, 2, result[2].ID)
}

func TestWriteSysMetrics(t *testing.T) {
	rec := httptest.NewRecorder()
	s := SysStat{
		CpuUtilPerCore: []float64{10.5, 20.0},
		MemTotalMB:     8192,
		MemUsedMB:      4096,
		MemFreeMB:      4096,
		SwapTotalMB:    2048,
		SwapUsedMB:     512,
		LoadAvg1:       1.5,
		LoadAvg5:       1.2,
		LoadAvg15:      0.9,
		NetIO: []NetIOStat{
			{Name: "eth0", BytesRecv: 1000, BytesSent: 2000},
		},
	}

	writeSysMetrics(rec, s)
	body := rec.Body.String()

	assert.Contains(t, body, `llamaswap_cpu_util_percent{core="0"} 10.5`)
	assert.Contains(t, body, `llamaswap_cpu_util_percent{core="1"} 20`)
	assert.Contains(t, body, "llamaswap_memory_total_bytes 8589934592")
	assert.Contains(t, body, "llamaswap_memory_used_bytes 4294967296")
	assert.Contains(t, body, "llamaswap_memory_free_bytes 4294967296")
	assert.Contains(t, body, "llamaswap_swap_total_bytes 2147483648")
	assert.Contains(t, body, "llamaswap_swap_used_bytes 536870912")
	assert.Contains(t, body, `llamaswap_load_average{interval="1m"} 1.5`)
	assert.Contains(t, body, `llamaswap_load_average{interval="5m"} 1.2`)
	assert.Contains(t, body, `llamaswap_load_average{interval="15m"} 0.9`)
	assert.Contains(t, body, `llamaswap_network_bytes_total{interface="eth0",direction="recv"} 1000`)
	assert.Contains(t, body, `llamaswap_network_bytes_total{interface="eth0",direction="sent"} 2000`)
}

func TestWriteSysMetrics_NoNetIO(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSysMetrics(rec, SysStat{CpuUtilPerCore: []float64{5.0}})
	body := rec.Body.String()
	assert.NotContains(t, body, "llamaswap_network_bytes_total")
}

func TestWriteGpuMetrics_Empty(t *testing.T) {
	rec := httptest.NewRecorder()
	writeGpuMetrics(rec, nil)
	assert.Empty(t, rec.Body.String())
}

func TestWriteGpuMetrics(t *testing.T) {
	rec := httptest.NewRecorder()
	gpus := []GpuStat{
		{
			ID:          0,
			Name:        "NVIDIA RTX 4090",
			UUID:        "GPU-1234",
			TempC:       75,
			GpuUtilPct:  85.5,
			MemUtilPct:  60.0,
			MemUsedMB:   8192,
			MemTotalMB:  24576,
			FanSpeedPct: 55.0,
			PowerDrawW:  300.5,
		},
	}

	writeGpuMetrics(rec, gpus)
	body := rec.Body.String()

	assert.Contains(t, body, `llamaswap_gpu_temperature_celsius{id="0",name="NVIDIA RTX 4090",uuid="GPU-1234"} 75`)
	assert.Contains(t, body, `llamaswap_gpu_vram_temperature_celsius{id="0",name="NVIDIA RTX 4090",uuid="GPU-1234"} 0`)
	assert.Contains(t, body, `llamaswap_gpu_util_percent{id="0",name="NVIDIA RTX 4090",uuid="GPU-1234"} 85.5`)
	assert.Contains(t, body, `llamaswap_gpu_memory_util_percent{id="0",name="NVIDIA RTX 4090",uuid="GPU-1234"} 60`)
	assert.Contains(t, body, `llamaswap_gpu_memory_used_bytes{id="0",name="NVIDIA RTX 4090",uuid="GPU-1234"}`)
	assert.Contains(t, body, `llamaswap_gpu_memory_total_bytes{id="0",name="NVIDIA RTX 4090",uuid="GPU-1234"}`)
	assert.Contains(t, body, `llamaswap_gpu_fan_speed_percent{id="0",name="NVIDIA RTX 4090",uuid="GPU-1234"} 55`)
	assert.Contains(t, body, `llamaswap_gpu_power_draw_watts{id="0",name="NVIDIA RTX 4090",uuid="GPU-1234"} 300.5`)
}

func TestWriteGpuMetrics_VramTemp(t *testing.T) {
	rec := httptest.NewRecorder()
	gpus := []GpuStat{
		{ID: 0, Name: "AMD RX 7900", UUID: "GPU-5678", TempC: 70, VramTempC: 85},
	}
	writeGpuMetrics(rec, gpus)
	body := rec.Body.String()
	assert.Contains(t, body, `llamaswap_gpu_temperature_celsius{id="0",name="AMD RX 7900",uuid="GPU-5678"} 70`)
	assert.Contains(t, body, `llamaswap_gpu_vram_temperature_celsius{id="0",name="AMD RX 7900",uuid="GPU-5678"} 85`)
}

func TestWriteGpuMetrics_EmptyUUID(t *testing.T) {
	rec := httptest.NewRecorder()
	gpus := []GpuStat{{ID: 3, Name: "AMD RX 7900", UUID: ""}}
	writeGpuMetrics(rec, gpus)
	body := rec.Body.String()
	assert.NotContains(t, body, "uuid=")
	assert.Contains(t, body, `name="AMD RX 7900"`)
}

func TestWriteGpuMetrics_LabelSanitization(t *testing.T) {
	rec := httptest.NewRecorder()
	gpus := []GpuStat{
		{ID: 0, Name: `GPU "special"`, UUID: "uuid\nline"},
	}
	writeGpuMetrics(rec, gpus)
	body := rec.Body.String()
	assert.Contains(t, body, `name="GPU \"special\""`)
	assert.Contains(t, body, `uuid="uuid\nline"`)
}

func TestMetricsHandler_ContentType(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.MetricsHandler()(rec, req)

	assert.Equal(t, "text/plain; version=0.0.4; charset=utf-8", rec.Header().Get("Content-Type"))
}

func TestMetricsHandler_EmptyStats(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.MetricsHandler()(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, strings.TrimSpace(rec.Body.String()))
}

func TestMetricsHandler_WithSysStats(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	m.sysRing.Push(SysStat{Timestamp: time.Now(), CpuUtilPerCore: []float64{25.0}, MemTotalMB: 4096, MemUsedMB: 2048, MemFreeMB: 2048})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.MetricsHandler()(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "llamaswap_cpu_util_percent")
	assert.Contains(t, body, "llamaswap_memory_total_bytes")
}

func TestMetricsHandler_UsesLatestSysStat(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	now := time.Now()
	m.sysRing.Push(SysStat{Timestamp: now.Add(-time.Second), MemTotalMB: 1000})
	m.sysRing.Push(SysStat{Timestamp: now, MemTotalMB: 8192})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.MetricsHandler()(rec, req)

	body := rec.Body.String()
	// 8192 MB = 8589934592 bytes
	assert.Contains(t, body, "llamaswap_memory_total_bytes 8589934592")
}

func TestMetricsHandler_WithGpuStats(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	m.gpuRing.Push([]GpuStat{{ID: 0, Name: "TestGPU", UUID: "uuid-0", TempC: 65, Timestamp: time.Now()}})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.MetricsHandler()(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "llamaswap_gpu_temperature_celsius")
	assert.Contains(t, body, `name="TestGPU"`)
}
