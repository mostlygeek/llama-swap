package perf

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseNvidiaSmiLine parses a single line from nvidia-smi CSV output.
// Format: index,name,uuid,temperature.gpu,utilization.gpu,memory.used,memory.total,fan.speed,power.draw
func ParseNvidiaSmiLine(line string) *GpuStat {
	fields := strings.Split(line, ",")
	if len(fields) < 9 {
		return nil
	}

	id, _ := strconv.Atoi(strings.TrimSpace(fields[0]))
	name := strings.TrimSpace(fields[1])
	uuid := strings.TrimSpace(fields[2])
	tempC, _ := strconv.Atoi(strings.TrimSpace(fields[3]))
	gpuUtil, _ := strconv.ParseFloat(strings.TrimSpace(fields[4]), 64)
	memUsed, _ := strconv.Atoi(strings.TrimSpace(fields[5]))
	memTotal, _ := strconv.Atoi(strings.TrimSpace(fields[6]))
	fanSpeed, _ := strconv.ParseFloat(strings.TrimSpace(fields[7]), 64)
	powerDraw, _ := strconv.ParseFloat(strings.TrimSpace(fields[8]), 64)

	var memUtil float64
	if memTotal > 0 {
		memUtil = float64(memUsed) / float64(memTotal) * 100
	}

	return &GpuStat{
		Timestamp:   time.Now(),
		ID:          id,
		Name:        name,
		UUID:        uuid,
		TempC:       tempC,
		GpuUtilPct:  gpuUtil,
		MemUtilPct:  memUtil,
		MemUsedMB:   memUsed,
		MemTotalMB:  memTotal,
		FanSpeedPct: fanSpeed,
		PowerDrawW:  powerDraw,
	}
}

// ParseIntelGpuTop parses one JSON sample object from intel_gpu_top -J output.
func ParseIntelGpuTop(data string) *GpuStat {
	var stats intelGpuTopStat
	if err := json.Unmarshal([]byte(data), &stats); err != nil {
		return nil
	}

	var gpuUtil float64
	for _, eng := range stats.Engines {
		if float64(eng.Busy) > gpuUtil {
			gpuUtil = float64(eng.Busy)
		}
	}
	// Fallback to 100% - RC6% if engine utilization is reported as 0
	if gpuUtil == 0 && float64(stats.RC6.Value) > 0 && float64(stats.RC6.Value) <= 100.0 {
		gpuUtil = 100.0 - float64(stats.RC6.Value)
	}

	var powerDraw float64
	if float64(stats.Power.GPU) > 0 {
		powerDraw = float64(stats.Power.GPU)
	} else if float64(stats.Power.Package) > 0 {
		powerDraw = float64(stats.Power.Package)
	}

	var memUsedBytes float64
	for _, client := range stats.Clients {
		for _, memType := range []string{"local", "system", "gtt"} {
			if memMap, ok := client.Memory[memType]; ok {
				if val, ok2 := memMap["resident"]; ok2 {
					memUsedBytes += float64(val)
				}
			}
		}
	}
	memUsedMB := int(memUsedBytes / 1024 / 1024)

	return &GpuStat{
		Timestamp:  time.Now(),
		ID:         0, // ponytail: intel_gpu_top monitors a single device
		Name:       "Intel GPU",
		GpuUtilPct: gpuUtil,
		MemUsedMB:  memUsedMB,
		PowerDrawW: powerDraw,
	}
}

type intelGpuTopStat struct {
	Period struct {
		Duration flexFloat64 `json:"duration"`
		Unit     string      `json:"unit"`
	} `json:"period"`
	Frequency struct {
		Requested flexFloat64 `json:"requested"`
		Actual    flexFloat64 `json:"actual"`
		Unit      string      `json:"unit"`
	} `json:"frequency"`
	Power struct {
		GPU     flexFloat64 `json:"GPU"`
		Package flexFloat64 `json:"Package"`
		Unit    string      `json:"unit"`
	} `json:"power"`
	RC6 struct {
		Value flexFloat64 `json:"value"`
		Unit  string      `json:"unit"`
	} `json:"rc6"`
	Engines map[string]intelGpuEngine `json:"engines"`
	Clients map[string]intelGpuClient `json:"clients"`
}

type intelGpuEngine struct {
	Busy flexFloat64 `json:"busy"`
	Sema flexFloat64 `json:"sema"`
	Wait flexFloat64 `json:"wait"`
	Unit string      `json:"unit"`
}

type intelGpuClient struct {
	Name          string                               `json:"name"`
	Pid           string                               `json:"pid"`
	EngineClasses map[string]intelGpuClientEngineClass `json:"engine-classes"`
	Memory        map[string]map[string]flexFloat64    `json:"memory"`
}

type intelGpuClientEngineClass struct {
	Busy flexFloat64 `json:"busy"`
	Unit string      `json:"unit"`
}

// flexFloat64 handles numbers serialized as JSON numbers or strings (e.g. "0.000000")
type flexFloat64 float64

func (f *flexFloat64) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	s = strings.Trim(s, `"`)
	if s == "" || s == "null" || s == "N/A" || s == "-" {
		*f = 0
		return nil
	}
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexFloat64(val)
	return nil
}

// mactopOutput maps the subset of mactop's headless JSON output that is
// relevant to GpuStat. Note that mactop's memory object is whole-system memory,
// not GPU-attributed; the darwin monitor overlays ioreg's GPU-attributed
// unified memory (see overlayIoregMem) so both backends report consistent
// memory figures.
type mactopOutput struct {
	SocMetrics struct {
		GPUPower float64 `json:"gpu_power"`
		GPUFreq  int     `json:"gpu_freq_mhz"`
		GPUTemp  float64 `json:"gpu_temp"`
	} `json:"soc_metrics"`
	Memory struct {
		Total uint64 `json:"total"`
		Used  uint64 `json:"used"`
	} `json:"memory"`
	GPUUsage   float64 `json:"gpu_usage"`
	SystemInfo struct {
		Name         string `json:"name"`
		GPUCoreCount int    `json:"gpu_core_count"`
	} `json:"system_info"`
	Fans []struct {
		RPM    int `json:"rpm"`
		MinRPM int `json:"min_rpm"`
		MaxRPM int `json:"max_rpm"`
	} `json:"fans"`
	Temperatures []struct {
		Group string  `json:"group"`
		Avg   float64 `json:"avg_celsius"`
	} `json:"temperatures"`
}

// ioreg output uses ` = ` (with spaces) for top-level device properties and
// `=` (no spaces) for values inside nested dictionaries such as
// PerformanceStatistics.
var (
	reIoregModel     = regexp.MustCompile(`"model"\s*=\s*"([^"]+)"`)
	reIoregCoreCount = regexp.MustCompile(`"gpu-core-count"\s*=\s*(\d+)`)
	reIoregUtil      = regexp.MustCompile(`"Device Utilization %"=(\d+)`)
	reIoregMemUsed   = regexp.MustCompile(`"In use system memory"=(\d+)`)
)

// ParseIoregOutput parses `ioreg -r -c IOGPU -d 1 -f` output into a GpuStat for
// the Apple Silicon integrated GPU. This is a fallback for when mactop is not
// installed: utilization and used memory are available, but power, temperature,
// and fan speed are not exposed by ioreg. memTotalMB is the unified memory size
// supplied by the caller, since Apple Silicon shares memory between CPU and GPU.
// Returns nil if no GPU device is found in the output.
func ParseIoregOutput(out []byte, memTotalMB int) *GpuStat {
	utilMatch := reIoregUtil.FindSubmatch(out)
	memMatch := reIoregMemUsed.FindSubmatch(out)
	if utilMatch == nil && memMatch == nil {
		return nil
	}

	var gpuUtil float64
	if utilMatch != nil {
		gpuUtil, _ = strconv.ParseFloat(string(utilMatch[1]), 64)
	}

	const toMB = 1024 * 1024
	var memUsedMB int
	if memMatch != nil {
		memUsedBytes, _ := strconv.ParseInt(string(memMatch[1]), 10, 64)
		memUsedMB = int(memUsedBytes / toMB)
	}

	var memUtil float64
	if memTotalMB > 0 {
		memUtil = float64(memUsedMB) / float64(memTotalMB) * 100
	}

	name := "Apple GPU"
	if m := reIoregModel.FindSubmatch(out); m != nil {
		name = string(m[1])
	}
	if m := reIoregCoreCount.FindSubmatch(out); m != nil {
		if cores, err := strconv.Atoi(string(m[1])); err == nil && cores > 0 {
			name = fmt.Sprintf("%s (%d-core GPU)", name, cores)
		}
	}

	return &GpuStat{
		Timestamp:  time.Now(),
		ID:         0,
		Name:       name,
		GpuUtilPct: gpuUtil,
		MemUtilPct: memUtil,
		MemUsedMB:  memUsedMB,
		MemTotalMB: memTotalMB,
	}
}

// ParseMactopLine parses a single line of mactop headless JSON output into a
// GpuStat for the Apple Silicon integrated GPU. Returns nil if the line cannot
// be parsed.
func ParseMactopLine(line string) *GpuStat {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var out mactopOutput
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		return nil
	}

	const toMB = 1024 * 1024
	memUsedMB := int(out.Memory.Used / toMB)
	memTotalMB := int(out.Memory.Total / toMB)

	var memUtil float64
	if memTotalMB > 0 {
		memUtil = float64(memUsedMB) / float64(memTotalMB) * 100
	}

	name := out.SystemInfo.Name
	if name == "" {
		name = "Apple GPU"
	}
	if out.SystemInfo.GPUCoreCount > 0 {
		name = fmt.Sprintf("%s (%d-core GPU)", name, out.SystemInfo.GPUCoreCount)
	}

	// Unified memory has no dedicated VRAM sensor; use the memory temperature
	// group when mactop exposes it.
	var vramTempC int
	for _, t := range out.Temperatures {
		if strings.EqualFold(t.Group, "Memory") {
			vramTempC = int(math.Round(t.Avg))
			break
		}
	}

	// Average fan load across all fans as a percentage of their RPM range.
	var fanSpeed float64
	var fanCount int
	for _, f := range out.Fans {
		if f.MaxRPM > f.MinRPM {
			pct := float64(f.RPM-f.MinRPM) / float64(f.MaxRPM-f.MinRPM) * 100
			if pct < 0 {
				pct = 0
			}
			fanSpeed += pct
			fanCount++
		}
	}
	if fanCount > 0 {
		fanSpeed /= float64(fanCount)
	}

	return &GpuStat{
		Timestamp:   time.Now(),
		ID:          0,
		Name:        name,
		TempC:       int(math.Round(out.SocMetrics.GPUTemp)),
		VramTempC:   vramTempC,
		GpuUtilPct:  out.GPUUsage,
		MemUtilPct:  memUtil,
		MemUsedMB:   memUsedMB,
		MemTotalMB:  memTotalMB,
		FanSpeedPct: fanSpeed,
		PowerDrawW:  out.SocMetrics.GPUPower,
	}
}
