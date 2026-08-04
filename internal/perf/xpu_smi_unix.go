//go:build unix && !darwin

package perf

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
)

const xpuSmiPollTimeout = 5 * time.Second

var xpuSmiFields = []string{
	"index",
	"name",
	"uuid",
	"temperature.gpu",
	"temperature.memory",
	"power.draw",
	"utilization.gpu",
	"utilization.compute",
	"utilization.render",
	"utilization.copy",
	"memory.used",
	"memory.total",
	"fan.speed",
	"eu.active",
	"eu.stall",
	"eu.idle",
	"memory.read.bandwidth",
	"memory.write.bandwidth",
	"memory.bandwidth.utilization",
	"pcie.rx.throughput",
	"pcie.tx.throughput",
	"clocks.current.graphics",
	"clocks.max.graphics",
	"clocks.current.media",
	"clocks.max.media",
	"power.limit",
	"energy.consumed",
}

type xpuSmiRunner interface {
	LookPath(file string) (string, error)
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type systemXpuSmiRunner struct{}

func (systemXpuSmiRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (systemXpuSmiRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

type xpuSmiProvider struct {
	runner xpuSmiRunner
	logger *logmon.Monitor
}

func tryXpuSmi(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	return (&xpuSmiProvider{runner: systemXpuSmiRunner{}, logger: logger}).start(ctx, every)
}

func (p *xpuSmiProvider) start(ctx context.Context, every time.Duration) (chan []GpuStat, error) {
	if _, err := p.runner.LookPath("xpu-smi"); err != nil {
		return nil, ErrNoGpuTool
	}

	if os.Geteuid() != 0 {
		p.logger.Warn("xpu-smi is running without root privileges; Intel temperature, bandwidth, PCIe, and engine utilization metrics may be unavailable")
	}

	if _, err := p.sample(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("xpu-smi probe failed: %w", err)
	}

	if every < time.Second {
		every = time.Second
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
				stats, err := p.sample(ctx)
				if err != nil {
					if ctx.Err() == nil {
						p.logger.Debugf("xpu-smi poll failed: %s", err)
					}
					continue
				}
				select {
				case ch <- stats:
				default:
				}
			}
		}
	}()

	return ch, nil
}

func (p *xpuSmiProvider) sample(ctx context.Context) ([]GpuStat, error) {
	pollCtx, cancel := context.WithTimeout(ctx, xpuSmiPollTimeout)
	defer cancel()

	args := []string{
		"--query-gpu=" + strings.Join(xpuSmiFields, ","),
		"--format=csv,nounits",
	}
	out, err := p.runner.Output(pollCtx, "xpu-smi", args...)
	if err != nil {
		if pollCtx.Err() != nil {
			return nil, pollCtx.Err()
		}
		return nil, fmt.Errorf("xpu-smi command failed: %w", err)
	}

	stats, err := ParseXpuSmiCSV(out)
	if err != nil {
		return nil, err
	}
	if len(stats) == 0 {
		return nil, ErrNoGpuTool
	}
	return stats, nil
}

// ParseXpuSmiCSV parses xpu-smi --query-gpu CSV output into GPU statistics.
func ParseXpuSmiCSV(out []byte) ([]GpuStat, error) {
	reader := csv.NewReader(strings.NewReader(string(out)))
	reader.Comment = '#'
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("xpu-smi returned empty output")
		}
		return nil, fmt.Errorf("read xpu-smi header: %w", err)
	}

	columns := make(map[string]int, len(header))
	for i, value := range header {
		columns[normalizeXpuSmiColumn(value)] = i
	}
	for _, required := range []string{"index", "name"} {
		if _, ok := columns[required]; !ok {
			return nil, fmt.Errorf("xpu-smi output missing %q column", required)
		}
	}

	stats := make([]GpuStat, 0)
	for rowNum := 2; ; rowNum++ {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read xpu-smi row %d: %w", rowNum, err)
		}
		if len(record) == 0 || allEmpty(record) {
			continue
		}

		stat, err := parseXpuSmiRecord(columns, record)
		if err != nil {
			return nil, fmt.Errorf("parse xpu-smi row %d: %w", rowNum, err)
		}
		stats = append(stats, stat)
	}
	if len(stats) == 0 {
		return nil, errors.New("xpu-smi returned no GPU devices")
	}
	return stats, nil
}

func parseXpuSmiRecord(columns map[string]int, record []string) (GpuStat, error) {
	value := func(column string) string {
		index, ok := columns[column]
		if !ok || index >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[index])
	}

	id, err := strconv.Atoi(value("index"))
	if err != nil {
		return GpuStat{}, fmt.Errorf("invalid index %q", value("index"))
	}
	name := value("name")
	if name == "" || isXpuSmiUnavailable(name) {
		return GpuStat{}, errors.New("missing GPU name")
	}

	memUsed, err := xpuSmiMB(value("memory.used"))
	if err != nil {
		return GpuStat{}, fmt.Errorf("invalid memory.used: %w", err)
	}
	memTotal, err := xpuSmiMB(value("memory.total"))
	if err != nil {
		return GpuStat{}, fmt.Errorf("invalid memory.total: %w", err)
	}

	stat := GpuStat{
		Timestamp:             time.Now(),
		ID:                    id,
		Name:                  name,
		UUID:                  unavailableXpuSmiValue(value("uuid")),
		MemUsedMB:             memUsed,
		MemTotalMB:            memTotal,
		ComputeUtilPct:        xpuSmiOptionalFloat(value("utilization.compute")),
		RenderUtilPct:         xpuSmiOptionalFloat(value("utilization.render")),
		CopyUtilPct:           xpuSmiOptionalFloat(value("utilization.copy")),
		EUActivePct:           xpuSmiOptionalFloat(value("eu.active")),
		EUStallPct:            xpuSmiOptionalFloat(value("eu.stall")),
		EUIdlePct:             xpuSmiOptionalFloat(value("eu.idle")),
		MemReadBandwidthKBps:  xpuSmiOptionalFloat(value("memory.read.bandwidth")),
		MemWriteBandwidthKBps: xpuSmiOptionalFloat(value("memory.write.bandwidth")),
		MemBandwidthUtilPct:   xpuSmiOptionalFloat(value("memory.bandwidth.utilization")),
		PcieRxMBps:            xpuSmiOptionalFloat(value("pcie.rx.throughput")),
		PcieTxMBps:            xpuSmiOptionalFloat(value("pcie.tx.throughput")),
		GraphicsClockMHz:      xpuSmiOptionalFloat(value("clocks.current.graphics")),
		GraphicsClockMaxMHz:   xpuSmiOptionalFloat(value("clocks.max.graphics")),
		MediaClockMHz:         xpuSmiOptionalFloat(value("clocks.current.media")),
		MediaClockMaxMHz:      xpuSmiOptionalFloat(value("clocks.max.media")),
		PowerLimitW:           xpuSmiOptionalFloat(value("power.limit")),
		EnergyConsumedJ:       xpuSmiOptionalFloat(value("energy.consumed")),
	}

	if temp := xpuSmiOptionalFloat(value("temperature.gpu")); temp != nil {
		stat.TempC = int(*temp)
	}
	if temp := xpuSmiOptionalFloat(value("temperature.memory")); temp != nil {
		stat.VramTempC = int(*temp)
	}
	if util := xpuSmiOptionalFloat(value("utilization.gpu")); util != nil {
		stat.GpuUtilPct = *util
	}
	if power := xpuSmiOptionalFloat(value("power.draw")); power != nil {
		stat.PowerDrawW = *power
	}
	if fan := xpuSmiOptionalFloat(value("fan.speed")); fan != nil {
		stat.FanSpeedPct = *fan
	}
	if stat.MemTotalMB > 0 {
		stat.MemUtilPct = float64(stat.MemUsedMB) / float64(stat.MemTotalMB) * 100
	}

	return stat, nil
}

func normalizeXpuSmiColumn(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func xpuSmiMB(value string) (int, error) {
	if isXpuSmiUnavailable(value) {
		return 0, nil
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%q", value)
	}
	return int(parsed), nil
}

func xpuSmiOptionalFloat(value string) *float64 {
	if isXpuSmiUnavailable(value) {
		return nil
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || parsed < 0 {
		return nil
	}
	return &parsed
}

func unavailableXpuSmiValue(value string) string {
	if isXpuSmiUnavailable(value) {
		return ""
	}
	return value
}

func isXpuSmiUnavailable(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || strings.EqualFold(value, "N/A") || strings.EqualFold(value, "unknown")
}

func allEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}
