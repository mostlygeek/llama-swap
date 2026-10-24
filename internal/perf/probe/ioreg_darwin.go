package probe

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// ioreg reads the Apple Silicon GPU from `ioreg -r -c IOGPU -d 1 -f`. It
// reports utilization and GPU-attributed memory, but not power, temperature
// or fan speed.
type ioreg struct {
	path        string
	run         runner
	systemMemMB func() *int
}

func newIoreg(opts Options) *ioreg {
	return &ioreg{path: cliPath(opts, "ioreg"), run: execRunner, systemMemMB: systemMemTotalMB}
}

func (p *ioreg) Name() string { return "ioreg" }

func (p *ioreg) Read(ctx context.Context) ([]GpuSample, error) {
	out, err := p.output(ctx)
	if err != nil {
		return nil, err
	}
	s, ok := parseIoregOutput(out)
	if !ok {
		return nil, errors.New("ioreg: no GPU device reported")
	}
	s.SharedTotalMB = p.systemMemMB()
	return []GpuSample{s}, nil
}

func (p *ioreg) output(ctx context.Context) ([]byte, error) {
	bin, err := lookPath(p.path)
	if err != nil {
		return nil, err
	}
	out, err := p.run(ctx, bin, "-r", "-c", "IOGPU", "-d", "1", "-f")
	if err != nil {
		return nil, fmt.Errorf("ioreg: %w", err)
	}
	return out, nil
}

// gpuMemUsedMB returns ioreg's GPU-attributed unified memory, for mactop.
func (p *ioreg) gpuMemUsedMB(ctx context.Context) *int {
	out, err := p.output(ctx)
	if err != nil {
		return nil
	}
	s, ok := parseIoregOutput(out)
	if !ok {
		return nil
	}
	return s.SharedUsedMB
}

// ioreg output uses ` = ` for top-level device properties and `=` inside
// nested dictionaries such as PerformanceStatistics.
var (
	reIoregModel     = regexp.MustCompile(`"model"\s*=\s*"([^"]+)"`)
	reIoregCoreCount = regexp.MustCompile(`"gpu-core-count"\s*=\s*(\d+)`)
	reIoregUtil      = regexp.MustCompile(`"Device Utilization %"=(\d+)`)
	reIoregMemUsed   = regexp.MustCompile(`"In use system memory"=(\d+)`)
)

// parseIoregOutput parses ioreg output into a sample without SharedTotalMB,
// which the caller fills from system RAM.
func parseIoregOutput(out []byte) (GpuSample, bool) {
	utilMatch := reIoregUtil.FindSubmatch(out)
	memMatch := reIoregMemUsed.FindSubmatch(out)
	if utilMatch == nil && memMatch == nil {
		return GpuSample{}, false
	}

	var model string
	if m := reIoregModel.FindSubmatch(out); m != nil {
		model = string(m[1])
	}
	var cores int
	if m := reIoregCoreCount.FindSubmatch(out); m != nil {
		cores, _ = strconv.Atoi(string(m[1]))
	}

	s := GpuSample{Key: AppleKey, Name: appleGpuName(model, cores), State: GpuStateOK}
	if utilMatch != nil {
		s.UtilPct = parseFloat(string(utilMatch[1]))
	}
	if memMatch != nil {
		s.SharedUsedMB = parseBytesMB(string(memMatch[1]))
	}
	return s, true
}
