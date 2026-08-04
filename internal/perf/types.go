package perf

import "time"

type GpuStat struct {
	Timestamp time.Time `json:"timestamp"`

	ID          int     `json:"id"`
	Name        string  `json:"name"`
	UUID        string  `json:"uuid"`
	TempC       int     `json:"temp_c"`
	VramTempC   int     `json:"vram_temp_c"`
	GpuUtilPct  float64 `json:"gpu_util_pct"`
	MemUtilPct  float64 `json:"mem_util_pct"`
	MemUsedMB   int     `json:"mem_used_mb"`
	MemTotalMB  int     `json:"mem_total_mb"`
	FanSpeedPct float64 `json:"fan_speed_pct"`
	PowerDrawW  float64 `json:"power_draw_w"`

	ComputeUtilPct        *float64 `json:"compute_util_pct,omitempty"`
	RenderUtilPct         *float64 `json:"render_util_pct,omitempty"`
	CopyUtilPct           *float64 `json:"copy_util_pct,omitempty"`
	EUActivePct           *float64 `json:"eu_active_pct,omitempty"`
	EUStallPct            *float64 `json:"eu_stall_pct,omitempty"`
	EUIdlePct             *float64 `json:"eu_idle_pct,omitempty"`
	MemReadBandwidthKBps  *float64 `json:"mem_read_bandwidth_kbps,omitempty"`
	MemWriteBandwidthKBps *float64 `json:"mem_write_bandwidth_kbps,omitempty"`
	MemBandwidthUtilPct   *float64 `json:"mem_bandwidth_util_pct,omitempty"`
	PcieRxMBps            *float64 `json:"pcie_rx_mbps,omitempty"`
	PcieTxMBps            *float64 `json:"pcie_tx_mbps,omitempty"`
	GraphicsClockMHz      *float64 `json:"graphics_clock_mhz,omitempty"`
	GraphicsClockMaxMHz   *float64 `json:"graphics_clock_max_mhz,omitempty"`
	MediaClockMHz         *float64 `json:"media_clock_mhz,omitempty"`
	MediaClockMaxMHz      *float64 `json:"media_clock_max_mhz,omitempty"`
	PowerLimitW           *float64 `json:"power_limit_w,omitempty"`
	EnergyConsumedJ       *float64 `json:"energy_consumed_j,omitempty"`
}

type NetIOStat struct {
	Name      string `json:"name"`
	BytesRecv uint64 `json:"bytes_recv"`
	BytesSent uint64 `json:"bytes_sent"`
}

type SysStat struct {
	Timestamp time.Time `json:"timestamp"`

	CpuUtilPerCore []float64   `json:"cpu_util_per_core"`
	MemTotalMB     int         `json:"mem_total_mb"`
	MemUsedMB      int         `json:"mem_used_mb"`
	MemFreeMB      int         `json:"mem_free_mb"`
	SwapTotalMB    int         `json:"swap_total_mb"`
	SwapUsedMB     int         `json:"swap_used_mb"`
	LoadAvg1       float64     `json:"load_avg_1"`
	LoadAvg5       float64     `json:"load_avg_5"`
	LoadAvg15      float64     `json:"load_avg_15"`
	NetIO          []NetIOStat `json:"net_io"`
}
