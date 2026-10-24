package probe

import (
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
)

var rocmSmiArgs = []string{
	"-i", "-P", "-t", "-f", "-u",
	"--showmemuse", "--showmeminfo", "vram", "gtt",
	"--showproductname", "--showbus", "--csv",
}

// rocmSmi reads AMD GPUs with one rocm-smi run per tick.
type rocmSmi struct {
	path          string
	skipSuspended bool
	run           runner
}

func newRocmSmi(opts Options) *rocmSmi {
	return &rocmSmi{path: cliPath(opts, "rocm-smi"), skipSuspended: opts.SkipSuspended, run: execRunner}
}

func (p *rocmSmi) Name() string { return "rocm-smi" }

func (p *rocmSmi) Read(ctx context.Context) ([]GpuSample, error) {
	bin, err := lookPath(p.path)
	if err != nil {
		return nil, err
	}

	// rocm-smi queries every GPU and has no reliable way to pick them by PCI
	// address, so it does not run while any AMD GPU is suspended. Awake AMD
	// GPUs are then left to lower-priority probes (lact, sysfs).
	if suspended, _ := checkSuspended(vendorAMD, "AMD GPU", p.skipSuspended); len(suspended) > 0 {
		return suspended, nil
	}

	out, err := p.run(ctx, bin, rocmSmiArgs...)
	if err != nil {
		return nil, fmt.Errorf("rocm-smi: %w", err)
	}
	return parseRocmSmiCSV(string(out)), nil
}

// parseRocmSmiCSV parses rocm-smi --csv output. Column names differ between
// rocm-smi versions, so values are looked up by header name.
func parseRocmSmiCSV(out string) []GpuSample {
	var header []string
	var samples []GpuSample
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		row, err := csv.NewReader(strings.NewReader(line)).Read()
		if err != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row[0]), "device") {
			header = row
			continue
		}
		if s, ok := parseRocmSmiRow(header, row); ok {
			samples = append(samples, s)
		}
	}
	return samples
}

func parseRocmSmiRow(header, row []string) (GpuSample, bool) {
	if len(header) == 0 || len(header) != len(row) {
		return GpuSample{}, false
	}
	f := make(map[string]string, len(row))
	for i := range row {
		f[strings.TrimSpace(header[i])] = strings.TrimSpace(row[i])
	}

	device := f["device"]
	index, err := strconv.Atoi(strings.TrimPrefix(device, "card"))
	if err != nil {
		return GpuSample{}, false
	}

	s := GpuSample{
		Key:           gpuKey(firstValue(f, "PCI Bus", "PCI Bus ID"), firstValue(f, "GUID", "Unique ID"), "rocm-smi", index),
		Name:          rocmSmiName(f, device),
		State:         GpuStateOK,
		TempC:         parseFloat(f["Temperature (Sensor edge) (C)"]),
		VramTempC:     parseFloat(f["Temperature (Sensor memory) (C)"]),
		FanPct:        parseFloat(f["Fan speed (%)"]),
		UtilPct:       parseFloat(f["GPU use (%)"]),
		VramTotalMB:   parseBytesMB(f["VRAM Total Memory (B)"]),
		VramUsedMB:    parseBytesMB(f["VRAM Total Used Memory (B)"]),
		SharedTotalMB: parseBytesMB(f["GTT Total Memory (B)"]),
		SharedUsedMB:  parseBytesMB(f["GTT Total Used Memory (B)"]),
	}
	// Some GPUs report only one of the two power columns; an N/A in one
	// must not hide the other.
	if s.PowerW = parseFloat(f["Average Graphics Package Power (W)"]); s.PowerW == nil {
		s.PowerW = parseFloat(f["Current Socket Graphics Package Power (W)"])
	}
	return s, true
}

func rocmSmiName(f map[string]string, device string) string {
	gfx := f["GFX Version"]
	for _, name := range []string{f["Card Series"], f["Card series"], f["Device Name"]} {
		if !isNA(name) {
			if gfx != "" && !isNA(gfx) {
				return name + " (" + gfx + ")"
			}
			return name
		}
	}
	return device
}

func firstValue(f map[string]string, names ...string) string {
	for _, n := range names {
		if v := f[n]; !isNA(v) {
			return v
		}
	}
	return ""
}
