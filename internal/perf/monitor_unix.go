//go:build unix && !darwin

package perf

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
)

func getGpuStats(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	if ch, err := tryLACT(ctx, every, logger); err == nil {
		logger.Info("using LACT for GPU monitoring")
		return ch, nil
	} else {
		logger.Debugf("LACT: %s", err.Error())
	}

	if ch, err := tryROCmSmi(ctx, every, logger); err == nil {
		logger.Info("using rocm-smi for GPU monitoring")
		return ch, nil
	} else {
		logger.Debugf("rocm-smi: %s", err.Error())
	}

	if ch, err := tryNvidiaSmi(ctx, every, logger); err == nil {
		logger.Info("using nvidia-smi for GPU monitoring")
		return ch, nil
	} else {
		logger.Debugf("nvidia-smi: %s", err.Error())
	}

	if ch, err := tryRocmSmi(ctx, every, logger); err == nil {
		logger.Info("using rocm-smi for GPU monitoring")
		return ch, nil
	} else {
		logger.Debugf("rocm-smi: %s", err.Error())
	}

	if ch, err := trySysfs(ctx, every, logger); err == nil {
		logger.Info("using sysfs for GPU monitoring")
		return ch, nil
	} else {
		logger.Debugf("sysfs: %s", err.Error())
	}

	return nil, ErrNoGpuTool
}

func tryLACT(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	socketPath := lactSocketPath()
	if socketPath == "" {
		return nil, ErrNoGpuTool
	}

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to LACT socket: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	devices, err := lactListDevices(conn)
	if err != nil {
		return nil, fmt.Errorf("LACT ListDevices failed: %w", err)
	}

	if len(devices) == 0 {
		return nil, fmt.Errorf("LACT returned no devices")
	}

	ch := make(chan []GpuStat, 1)

	go func() {
		defer close(ch)
		ticker := time.NewTicker(every)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				socketPath := lactSocketPath()
				if socketPath == "" {
					continue
				}

				conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
				if err != nil {
					continue
				}
				conn.SetDeadline(time.Now().Add(5 * time.Second))

				devices, err := lactListDevices(conn)
				if err != nil {
					conn.Close()
					continue
				}

				stats := make([]GpuStat, 0, len(devices))
				for i, d := range devices {
					stat, err := lactGetDeviceStats(conn, d.ID, d.Name, i)
					if err != nil {
						continue
					}
					if stat.MemTotalMB == 0 {
						continue
					}
					stats = append(stats, stat)
				}
				conn.Close()

				if len(stats) > 0 {
					select {
					case ch <- stats:
					default:
					}
				}
			}
		}
	}()

	return ch, nil
}

func tryROCmSmi(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	if _, err := exec.LookPath("rocm-smi"); err != nil {
		return nil, ErrNoGpuTool
	}

	// First check if rocm-smi can see any GPUs
	checkCmd := exec.CommandContext(ctx, "rocm-smi", "--alldevices")
	checkCmd.Env = append(os.Environ(), "HSA_OVERRIDE_GFX_VERSION=12.0.1")
	if out, err := checkCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("rocm-smi check failed: %s: %w", string(out), err)
	}

	sec := int(every.Seconds())
	if sec < 2 {
		sec = 2
	}

	ch := make(chan []GpuStat, 1)

	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// rocm-smi doesn't support combining flags in one call, so query each metric separately
			// and merge the results.
			var stats []GpuStat

			// VRAM
			if out, err := exec.CommandContext(ctx, "rocm-smi", "--showmeminfo=vram", "--json").Output(); err == nil {
				stats = mergeROCmStats(stats, parseROCmSmiJSON(out))
			}

			// Temperature
			if out, err := exec.CommandContext(ctx, "rocm-smi", "--showtemp", "--json").Output(); err == nil {
				stats = mergeROCmStats(stats, parseROCmSmiJSON(out))
			}

			// Power
			if out, err := exec.CommandContext(ctx, "rocm-smi", "--showpower", "--json").Output(); err == nil {
				stats = mergeROCmStats(stats, parseROCmSmiJSON(out))
			}

			// GPU utilization
			if out, err := exec.CommandContext(ctx, "rocm-smi", "--showuse", "--json").Output(); err == nil {
				stats = mergeROCmStats(stats, parseROCmSmiJSON(out))
			}

			if len(stats) > 0 {
				select {
				case ch <- stats:
				default:
				}
			}

			timer := time.NewTimer(time.Duration(sec) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()

	return ch, nil
}

func parseROCmSmiJSON(out []byte) []GpuStat {
	// rocm-smi --json returns flat key-value pairs per card:
	// {"card0": {"VRAM Total Memory (B)": "34208743424", "VRAM Total Used Memory (B)": "31978942464"}}
	// Keys vary by version; parse as raw map for flexibility.
	var raw map[string]map[string]string
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil
	}

	if len(raw) == 0 {
		return nil
	}

	// Collect all card indices and their key-value maps.
	type cardData struct {
		idx   int
		keys  map[string]string
		total uint64
		used  uint64
		temp  float64
		power float64
		util  float64
	}
	cards := make(map[int]*cardData)

	for cardKey, kv := range raw {
		// Extract card index from "card0", "card1", etc.
		idx := 0
		fmt.Sscanf(cardKey, "card%d", &idx)

		cd, ok := cards[idx]
		if !ok {
			cd = &cardData{idx: idx, keys: kv}
			cards[idx] = cd
		}

		for key, val := range kv {
			// Clean up the value string (remove quotes if present)
			val = strings.Trim(val, "\"")
			var num float64
			fmt.Sscanf(val, "%f", &num)

			switch {
			case strings.Contains(key, "VRAM") && strings.Contains(key, "Total Memory") && !strings.Contains(key, "Used"):
				cd.total = uint64(num)
			case strings.Contains(key, "VRAM") && strings.Contains(key, "Used Memory"):
				cd.used = uint64(num)
			case strings.Contains(key, "Temperature") && strings.Contains(key, "edge"):
				// Prefer edge sensor (die edge); always overwrite.
				cd.temp = num
			case strings.Contains(key, "Temperature") && cd.temp == 0:
				// Fall back to any temperature sensor if edge isn't reported.
				cd.temp = num
			case strings.Contains(key, "GPU use") || (strings.Contains(key, "GPU") && strings.Contains(key, "use")):
				cd.util = num
			case strings.Contains(key, "Power") && strings.Contains(key, "W"):
				cd.power = num
			}
		}
	}

	var stats []GpuStat
	for _, cd := range cards {
		memTotalMB := int(cd.total / 1024 / 1024)
		memUsedMB := int(cd.used / 1024 / 1024)
		var memUtilPct float64
		if memTotalMB > 0 {
			memUtilPct = float64(memUsedMB) / float64(memTotalMB) * 100
		}
		gpuUtil := cd.util
		if gpuUtil == 0 && memTotalMB > 0 {
			gpuUtil = memUtilPct // fallback: approximate GPU util from VRAM utilization
		}
		stats = append(stats, GpuStat{
			ID:         cd.idx,
			Name:       fmt.Sprintf("AMD GPU [%d]", cd.idx),
			TempC:      int(cd.temp),
			GpuUtilPct: gpuUtil,
			MemUtilPct: memUtilPct,
			MemUsedMB:  memUsedMB,
			MemTotalMB: memTotalMB,
			PowerDrawW: cd.power,
		})
	}

	return stats
}

func mergeROCmStats(existing []GpuStat, newStats []GpuStat) []GpuStat {
	if len(newStats) == 0 {
		return existing
	}
	if len(existing) == 0 {
		return newStats
	}
	newMap := make(map[int]GpuStat)
	for _, s := range newStats {
		newMap[s.ID] = s
	}
	for i, s := range existing {
		ns, ok := newMap[s.ID]
		if !ok {
			continue
		}
		if ns.TempC != 0 {
			existing[i].TempC = ns.TempC
		}
		if ns.PowerDrawW != 0 {
			existing[i].PowerDrawW = ns.PowerDrawW
		}
		if ns.GpuUtilPct != 0 {
			existing[i].GpuUtilPct = ns.GpuUtilPct
		}
		if ns.MemTotalMB != 0 {
			existing[i].MemTotalMB = ns.MemTotalMB
		}
		if ns.MemUsedMB != 0 {
			existing[i].MemUsedMB = ns.MemUsedMB
		}
	}
	return existing
}

func parseROCmSmiCSV(out []byte) []GpuStat {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return nil
	}

	var stats []GpuStat
	// Parse: GPU[0] : VRAM Total Memory (B): 34208743424
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "VRAM") {
			continue
		}

		// Extract GPU index
		gpuIdx := 0
		if idx := strings.Index(line, "GPU["); idx >= 0 {
			end := strings.Index(line[idx:], "]")
			if end > 0 {
				fmt.Sscanf(line[idx+4:end], "%d", &gpuIdx)
			}
		}

		// Extract value
		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			continue
		}
		valStr := strings.TrimSpace(parts[len(parts)-1])
		var bytes uint64
		fmt.Sscanf(valStr, "%d", &bytes)

		memTotalMB := int(bytes / 1024 / 1024)
		if len(stats) <= gpuIdx {
			stats = append(stats, GpuStat{ID: gpuIdx, MemTotalMB: memTotalMB})
		} else {
			stats[gpuIdx].MemUsedMB = int(bytes / 1024 / 1024)
		}
	}

	// Second pass: if we got total and used on separate lines, compute utilization
	for i := range stats {
		if stats[i].MemTotalMB > 0 && stats[i].MemUsedMB > 0 {
			stats[i].MemUtilPct = float64(stats[i].MemUsedMB) / float64(stats[i].MemTotalMB) * 100
		}
	}

	return stats
}

func tryNvidiaSmi(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil, ErrNoGpuTool
	}

	sec := int(every.Seconds())
	if sec < 1 {
		sec = 1
	}

	cmd := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=index,name,uuid,temperature.gpu,utilization.gpu,memory.used,memory.total,fan.speed,power.draw",
		"--format=csv,noheader,nounits",
		"--loop", fmt.Sprintf("%d", sec),
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi stdout pipe failed: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("nvidia-smi start failed: %w", err)
	}

	ch := make(chan []GpuStat, 1)

	go func() {
		defer close(ch)

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			stat := ParseNvidiaSmiLine(line)
			if stat != nil {
				select {
				case ch <- []GpuStat{*stat}:
				default:
				}
			}
		}
		cmd.Wait()
	}()

	return ch, nil
}

func tryRocmSmi(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	if _, err := exec.LookPath("rocm-smi"); err != nil {
		return nil, ErrNoGpuTool
	}
	if every < time.Second {
		every = time.Second
	}
	const pollTimeout = 5 * time.Second

	ch := make(chan []GpuStat, 1)

	go func() {
		defer close(ch)
		ticker := time.NewTicker(every)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
				cmd := exec.CommandContext(pollCtx, "rocm-smi", "-i", "-P", "-t", "-f", "-u", "--showmemuse", "--showmeminfo", "vram", "--showproductname", "--csv")
				out, err := cmd.Output()
				timedOut := pollCtx.Err() == context.DeadlineExceeded
				cancel()
				if err != nil {
					if timedOut {
						logger.Debug("rocm-smi timed out")
					}
					continue
				}

				stats := make([]GpuStat, 0)
				scanner := bufio.NewScanner(strings.NewReader(string(out)))
				var header string
				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					if line == "" {
						continue
					}
					if strings.HasPrefix(line, "device,") {
						header = line
						continue
					}

					stat := parseRocmSmiLine(header, line)
					if stat != nil {
						stats = append(stats, *stat)
					}
				}

				if len(stats) > 0 {
					select {
					case ch <- stats:
					default:
					}
				}
			}
		}
	}()

	return ch, nil
}

func parseRocmSmiLine(header string, line string) *GpuStat {
	if header == "" || line == "" {
		return nil
	}
	labels := strings.Split(header, ",")
	fields := strings.Split(line, ",")
	if len(labels) != len(fields) {
		return nil
	}

	result := &GpuStat{
		Timestamp: time.Now(),
		ID:        -1,
	}

	var device string
	var deviceName string
	var cardSeries string
	var gfxVersion string

	const toMB = 1024 * 1024

	for i, col := range labels {
		val := strings.TrimSpace(fields[i])
		switch col {
		case "device":
			device = val
			id, err := strconv.Atoi(strings.TrimPrefix(val, "card"))
			if err != nil {
				return nil
			}
			result.ID = id
		case "Device Name":
			deviceName = val
		case "GUID":
			result.UUID = val
		case "Temperature (Sensor edge) (C)":
			tempC, _ := strconv.ParseFloat(val, 64)
			result.TempC = int(tempC)
		case "Temperature (Sensor memory) (C)":
			vramTempC, _ := strconv.ParseFloat(val, 64)
			result.VramTempC = int(vramTempC)
		case "Fan speed (%)":
			fanSpeed, _ := strconv.ParseFloat(val, 64)
			result.FanSpeedPct = fanSpeed
		case "Current Socket Graphics Package Power (W)":
			fallthrough
		case "Average Graphics Package Power (W)":
			powerDraw, _ := strconv.ParseFloat(val, 64)
			result.PowerDrawW = powerDraw
		case "GPU use (%)":
			gpuUtil, _ := strconv.ParseFloat(val, 64)
			result.GpuUtilPct = gpuUtil
		case "GPU Memory Allocated (VRAM%)":
			memUtil, _ := strconv.ParseFloat(val, 64)
			result.MemUtilPct = memUtil
		case "VRAM Total Memory (B)":
			memTotal, _ := strconv.ParseUint(val, 10, 64)
			result.MemTotalMB = int(memTotal / toMB)
		case "VRAM Total Used Memory (B)":
			memUsed, _ := strconv.ParseUint(val, 10, 64)
			result.MemUsedMB = int(memUsed / toMB)
		case "Card Series":
			cardSeries = val
		case "GFX Version":
			gfxVersion = val
		}
	}

	if result.ID == -1 {
		return nil
	}

	name := device
	if cardSeries != "" && cardSeries != "N/A" {
		name = cardSeries + " " + device + " (" + gfxVersion + ")"
	} else if deviceName != "" && deviceName != "N/A" {
		name = deviceName + " " + device + " (" + gfxVersion + ")"
	}
	result.Name = name

	return result
}

func trySysfs(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	return nil, ErrNotImplemented
}

func lactSocketPath() string {
	if p := os.Getenv("LACT_DAEMON_SOCKET_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	rootPath := "/run/lactd.sock"
	if _, err := os.Stat(rootPath); err == nil {
		return rootPath
	}

	u, err := user.Current()
	if err != nil {
		return ""
	}
	userPath := filepath.Join("/run/user", u.Uid, "lactd.sock")
	if _, err := os.Stat(userPath); err == nil {
		return userPath
	}

	return ""
}

type lactRequest struct {
	Command string      `json:"command"`
	Args    interface{} `json:"args,omitempty"`
}

type lactResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
}

type lactDeviceEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type lactDeviceStats struct {
	Fan struct {
		PwmCurrent *uint8 `json:"pwm_current"`
	} `json:"fan"`
	Vram struct {
		Total *uint64 `json:"total"`
		Used  *uint64 `json:"used"`
	} `json:"vram"`
	Power struct {
		Average *float64 `json:"average"`
		Current *float64 `json:"current"`
	} `json:"power"`
	Temps       map[string]lactTempEntry `json:"temps"`
	BusyPercent *uint8                   `json:"busy_percent"`
}

type lactTempEntry struct {
	Current *float64 `json:"current"`
}

func lactSendRequest(conn net.Conn, req lactRequest) (json.RawMessage, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	var resp lactResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}

	if resp.Status != "ok" {
		return nil, fmt.Errorf("LACT error: %s", string(resp.Data))
	}

	return resp.Data, nil
}

func lactListDevices(conn net.Conn) ([]lactDeviceEntry, error) {
	data, err := lactSendRequest(conn, lactRequest{Command: "list_devices"})
	if err != nil {
		return nil, err
	}

	var devices []lactDeviceEntry
	if err := json.Unmarshal(data, &devices); err != nil {
		return nil, err
	}

	return devices, nil
}

func lactGetDeviceStats(conn net.Conn, id string, name string, index int) (GpuStat, error) {
	data, err := lactSendRequest(conn, lactRequest{
		Command: "device_stats",
		Args: struct {
			ID string `json:"id"`
		}{ID: id},
	})
	if err != nil {
		return GpuStat{}, err
	}

	var stats lactDeviceStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return GpuStat{}, err
	}

	var memUsedMB, memTotalMB int
	if stats.Vram.Used != nil {
		memUsedMB = int(*stats.Vram.Used / 1024 / 1024)
	}
	if stats.Vram.Total != nil {
		memTotalMB = int(*stats.Vram.Total / 1024 / 1024)
	}

	var memUtil float64
	if memTotalMB > 0 {
		memUtil = float64(memUsedMB) / float64(memTotalMB) * 100
	}

	var gpuUtil float64
	if stats.BusyPercent != nil {
		gpuUtil = float64(*stats.BusyPercent)
	}

	var fanSpeed float64
	if stats.Fan.PwmCurrent != nil {
		fanSpeed = float64(*stats.Fan.PwmCurrent) / 255.0 * 100.0
	}

	var powerDraw float64
	if stats.Power.Average != nil && *stats.Power.Average > 0 {
		powerDraw = *stats.Power.Average
	} else if stats.Power.Current != nil {
		powerDraw = *stats.Power.Current
	}

	var tempC int
	if t, ok := stats.Temps["edge"]; ok && t.Current != nil {
		tempC = int(*t.Current)
	} else if t, ok := stats.Temps["junction"]; ok && t.Current != nil {
		tempC = int(*t.Current)
	} else {
		for _, t := range stats.Temps {
			if t.Current != nil {
				tempC = int(*t.Current)
				break
			}
		}
	}

	var vramTempC int
	// nvidia uses "VRAM", amd "mem"
	for _, key := range []string{"mem", "VRAM"} {
		if t, ok := stats.Temps[key]; ok && t.Current != nil && *t.Current > 0 {
			vramTempC = int(*t.Current)
			break
		}
	}

	return GpuStat{
		Timestamp:   time.Now(),
		ID:          index,
		Name:        name,
		UUID:        id,
		TempC:       tempC,
		VramTempC:   vramTempC,
		GpuUtilPct:  gpuUtil,
		MemUtilPct:  memUtil,
		MemUsedMB:   memUsedMB,
		MemTotalMB:  memTotalMB,
		FanSpeedPct: fanSpeed,
		PowerDrawW:  powerDraw,
	}, nil
}

func readSysfs() ([]GpuStat, error) {
	return nil, ErrNotImplemented
}

func readSysStats() (SysStat, error) {
	cpuPcts, err := cpu.Percent(0, true)
	if err != nil {
		return SysStat{}, err
	}

	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return SysStat{}, err
	}

	const toMB = 1024 * 1024

	var swapTotalMB, swapUsedMB int
	if swapStat, err := mem.SwapMemory(); err == nil {
		swapTotalMB = int(swapStat.Total / toMB)
		swapUsedMB = int(swapStat.Used / toMB)
	}

	var loadAvg1, loadAvg5, loadAvg15 float64
	if loadStat, err := load.Avg(); err == nil {
		loadAvg1 = loadStat.Load1
		loadAvg5 = loadStat.Load5
		loadAvg15 = loadStat.Load15
	}

	netIO := make([]NetIOStat, 0)
	if ioCounters, err := psnet.IOCounters(true); err == nil {
		for _, ioc := range ioCounters {
			if ioc.Name == "lo" {
				continue
			}
			netIO = append(netIO, NetIOStat{
				Name:      ioc.Name,
				BytesRecv: ioc.BytesRecv,
				BytesSent: ioc.BytesSent,
			})
		}
	}

	return SysStat{
		Timestamp:      time.Now(),
		CpuUtilPerCore: cpuPcts,
		MemTotalMB:     int(vmStat.Total / toMB),
		MemUsedMB:      int(vmStat.Used / toMB),
		MemFreeMB:      int(vmStat.Free / toMB),
		SwapTotalMB:    swapTotalMB,
		SwapUsedMB:     swapUsedMB,
		LoadAvg1:       loadAvg1,
		LoadAvg5:       loadAvg5,
		LoadAvg15:      loadAvg15,
		NetIO:          netIO,
	}, nil
}
