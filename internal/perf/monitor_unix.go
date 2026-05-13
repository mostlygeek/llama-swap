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

	if ch, err := tryNvidiaSmi(ctx, every, logger); err == nil {
		logger.Info("using nvidia-smi for GPU monitoring")
		return ch, nil
	} else {
		logger.Debugf("nvidia-smi: %s", err.Error())
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
		"-loop", fmt.Sprintf("%d", sec),
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

			stat := parseNvidiaSmiLine(line)
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

func parseNvidiaSmiLine(line string) *GpuStat {
	fields := strings.Split(line, ", ")
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
