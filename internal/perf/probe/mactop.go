package probe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strings"
)

// mactopIntervalMs is the sampling window mactop measures power over.
const mactopIntervalMs = "1000"

// mactop reads the Apple Silicon GPU from mactop's headless JSON output. See
// https://github.com/metaspartan/mactop.
type mactop struct {
	path string
	run  runner
	// gpuMemUsedMB returns GPU-attributed unified memory. mactop only
	// reports whole-system memory, so macOS wires this to ioreg.
	gpuMemUsedMB func(ctx context.Context) *int
}

func newMactop(opts Options) *mactop {
	return &mactop{path: cliPath(opts, "mactop"), run: runFirstLine}
}

func (p *mactop) Name() string { return "mactop" }

func (p *mactop) Read(ctx context.Context) ([]GpuSample, error) {
	bin, err := lookPath(p.path)
	if err != nil {
		return nil, err
	}
	out, err := p.run(ctx, bin, "--headless", "--format", "json", "--interval", mactopIntervalMs)
	if err != nil {
		return nil, fmt.Errorf("mactop: %w", err)
	}
	s, ok := parseMactopLine(string(out))
	if !ok {
		return nil, errors.New("mactop: unreadable output")
	}
	if p.gpuMemUsedMB != nil {
		s.SharedUsedMB = p.gpuMemUsedMB(ctx)
	}
	return []GpuSample{s}, nil
}

// mactopOutput is the subset of mactop's headless JSON that is used.
type mactopOutput struct {
	SocMetrics struct {
		GPUPower float64 `json:"gpu_power"`
		GPUTemp  float64 `json:"gpu_temp"`
	} `json:"soc_metrics"`
	Memory struct {
		Total uint64 `json:"total"`
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

// parseMactopLine parses one line of mactop headless JSON output.
func parseMactopLine(line string) (GpuSample, bool) {
	var out mactopOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &out); err != nil {
		return GpuSample{}, false
	}

	s := GpuSample{
		Key:     AppleKey,
		Name:    appleGpuName(out.SystemInfo.Name, out.SystemInfo.GPUCoreCount),
		State:   GpuStateOK,
		UtilPct: ptr(out.GPUUsage),
		PowerW:  ptr(out.SocMetrics.GPUPower),
		TempC:   ptr(math.Round(out.SocMetrics.GPUTemp)),
	}
	if out.Memory.Total > 0 {
		s.SharedTotalMB = ptr(int(out.Memory.Total / bytesPerMB))
	}
	// Unified memory has no VRAM sensor; use the memory temperature group.
	for _, t := range out.Temperatures {
		if strings.EqualFold(t.Group, "Memory") {
			s.VramTempC = ptr(math.Round(t.Avg))
			break
		}
	}
	// Average fan load across all fans as a share of their RPM range.
	var fan float64
	var fans int
	for _, f := range out.Fans {
		if f.MaxRPM > f.MinRPM {
			fan += max(0, float64(f.RPM-f.MinRPM)/float64(f.MaxRPM-f.MinRPM)*100)
			fans++
		}
	}
	if fans > 0 {
		s.FanPct = ptr(fan / float64(fans))
	}
	return s, true
}

// appleGpuName formats the Apple Silicon GPU name the same way for mactop
// and ioreg so the merged GPU keeps one name.
func appleGpuName(model string, cores int) string {
	if model == "" {
		model = "Apple GPU"
	}
	if cores > 0 {
		return fmt.Sprintf("%s (%d-core GPU)", model, cores)
	}
	return model
}

// runFirstLine starts a streaming CLI, returns its first non-empty line and
// stops it.
func runFirstLine(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	scanner := bufio.NewScanner(stdout)
	// mactop's JSON objects can be large.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			return []byte(line), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("no output")
}
