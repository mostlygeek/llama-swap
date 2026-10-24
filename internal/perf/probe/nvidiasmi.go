package probe

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/mem"
)

// nvidiaSmiQuery is the --query-gpu field list. parseNvidiaSmiLine depends on
// this order.
const nvidiaSmiQuery = "index,name,uuid,pci.bus_id,temperature.gpu,utilization.gpu,memory.used,memory.total,fan.speed,power.draw"

// unifiedMemoryGpuNames match NVIDIA GPUs that share system RAM instead of
// having a framebuffer. nvidia-smi reports their memory as N/A.
var unifiedMemoryGpuNames = []string{"GB10", "GB200", "GB300", "GH200", "JETSON", "TEGRA", "ORIN", "THOR", "XAVIER"}

// nvidiaSmi reads NVIDIA GPUs with one nvidia-smi run per tick.
type nvidiaSmi struct {
	path          string
	skipSuspended bool
	run           runner
	// systemMemMB returns system RAM total, used as the memory ceiling of
	// unified-memory GPUs.
	systemMemMB func() *int
}

func newNvidiaSmi(opts Options) *nvidiaSmi {
	return &nvidiaSmi{
		path:          cliPath(opts, "nvidia-smi"),
		skipSuspended: opts.SkipSuspended,
		run:           execRunner,
		systemMemMB:   systemMemTotalMB,
	}
}

func (p *nvidiaSmi) Name() string { return "nvidia-smi" }

func (p *nvidiaSmi) Read(ctx context.Context) ([]GpuSample, error) {
	bin, err := lookPath(p.path)
	if err != nil {
		return nil, err
	}

	// nvidia-smi wakes every GPU it queries, so only ask for awake ones.
	suspended, awake := checkSuspended(vendorNVIDIA, "NVIDIA GPU", p.skipSuspended)
	args := []string{"--query-gpu=" + nvidiaSmiQuery, "--format=csv,noheader,nounits"}
	if len(suspended) > 0 {
		if len(awake) == 0 {
			return suspended, nil
		}
		ids := make([]string, len(awake))
		for i, key := range awake {
			ids[i] = "0000" + key // nvidia-smi's 8-digit domain form
		}
		args = append(args, "--id="+strings.Join(ids, ","))
	}

	out, err := p.run(ctx, bin, args...)
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi: %w", err)
	}

	samples := suspended
	unified := false
	for _, line := range strings.Split(string(out), "\n") {
		s, ok := parseNvidiaSmiLine(line)
		if !ok {
			continue
		}
		if s.VramTotalMB == nil && isUnifiedMemoryGpu(s.Name) {
			unified = true
			s.SharedTotalMB = p.systemMemMB()
		}
		samples = append(samples, s)
	}
	if unified {
		if used, ok := p.computeAppsMemMB(ctx, bin); ok {
			for i := range samples {
				if samples[i].SharedTotalMB != nil {
					samples[i].SharedUsedMB = ptr(used)
				}
			}
		}
	}
	return samples, nil
}

// computeAppsMemMB sums per-process GPU memory. It is the memory used figure
// on unified-memory GPUs, where memory.used is N/A.
func (p *nvidiaSmi) computeAppsMemMB(ctx context.Context, bin string) (int, bool) {
	out, err := p.run(ctx, bin, "--query-compute-apps=used_memory", "--format=csv,noheader,nounits")
	if err != nil {
		return 0, false
	}
	total := 0
	for _, line := range strings.Split(string(out), "\n") {
		if v, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			total += v
		}
	}
	return total, true
}

// parseNvidiaSmiLine parses one line of nvidiaSmiQuery CSV output.
func parseNvidiaSmiLine(line string) (GpuSample, bool) {
	fields := strings.Split(line, ",")
	if len(fields) < 10 {
		return GpuSample{}, false
	}
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	index, err := strconv.Atoi(fields[0])
	if err != nil {
		return GpuSample{}, false
	}
	return GpuSample{
		Key:         gpuKey(fields[3], fields[2], "nvidia-smi", index),
		Name:        fields[1],
		State:       GpuStateOK,
		TempC:       parseFloat(fields[4]),
		UtilPct:     parseFloat(fields[5]),
		VramUsedMB:  parseMB(fields[6]),
		VramTotalMB: parseMB(fields[7]),
		FanPct:      parseFloat(fields[8]),
		PowerW:      parseFloat(fields[9]),
	}, true
}

func isUnifiedMemoryGpu(name string) bool {
	upper := strings.ToUpper(name)
	for _, n := range unifiedMemoryGpuNames {
		if strings.Contains(upper, n) {
			return true
		}
	}
	return false
}

// systemMemTotalMB returns total system RAM, or nil when it can't be read.
func systemMemTotalMB() *int {
	v, err := mem.VirtualMemory()
	if err != nil || v.Total == 0 {
		return nil
	}
	return ptr(int(v.Total / bytesPerMB))
}
