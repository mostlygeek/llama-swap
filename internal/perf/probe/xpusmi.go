package probe

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// xpuSmiFields are requested from xpu-smi. Columns are read back by header
// name, so the order does not matter.
var xpuSmiFields = []string{
	"index", "name", "uuid",
	"temperature.gpu", "temperature.memory", "power.draw",
	"utilization.gpu", "memory.used", "memory.total", "fan.speed",
}

// xpuSmi reads Intel GPUs with one xpu-smi run per tick.
type xpuSmi struct {
	path          string
	skipSuspended bool
	run           runner
	// pciKeys returns the PCI keys of Intel GPUs in PCI order. xpu-smi does
	// not print PCI addresses, so they are matched by index when the counts
	// agree.
	pciKeys func() []string
}

func newXpuSmi(opts Options) *xpuSmi {
	return &xpuSmi{
		path:          cliPath(opts, "xpu-smi"),
		skipSuspended: opts.SkipSuspended,
		run:           execRunner,
		pciKeys:       intelPCIKeys,
	}
}

func (p *xpuSmi) Name() string { return "xpu-smi" }

func (p *xpuSmi) Read(ctx context.Context) ([]GpuSample, error) {
	bin, err := lookPath(p.path)
	if err != nil {
		return nil, err
	}
	// Like rocm-smi, xpu-smi queries every GPU, so it does not run while
	// any Intel GPU is suspended.
	if suspended, _ := checkSuspended(vendorIntel, "Intel GPU", p.skipSuspended); len(suspended) > 0 {
		return suspended, nil
	}

	out, err := p.run(ctx, bin, "--query-gpu="+strings.Join(xpuSmiFields, ","), "--format=csv,nounits")
	if err != nil {
		return nil, fmt.Errorf("xpu-smi: %w", err)
	}
	samples, err := parseXpuSmiCSV(out)
	if err != nil {
		return nil, err
	}
	if keys := p.pciKeys(); len(keys) == len(samples) {
		for i := range samples {
			samples[i].Key = keys[i]
		}
	}
	return samples, nil
}

// parseXpuSmiCSV parses xpu-smi --query-gpu CSV output.
func parseXpuSmiCSV(out []byte) ([]GpuSample, error) {
	reader := csv.NewReader(strings.NewReader(string(out)))
	reader.Comment = '#'
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("xpu-smi: empty output")
		}
		return nil, fmt.Errorf("xpu-smi: reading header: %w", err)
	}
	columns := make(map[string]int, len(header))
	for i, name := range header {
		columns[strings.ToLower(strings.TrimSpace(name))] = i
	}

	var samples []GpuSample
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xpu-smi: %w", err)
		}
		value := func(column string) string {
			i, ok := columns[column]
			if !ok || i >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[i])
		}
		index, err := strconv.Atoi(value("index"))
		if err != nil || isNA(value("name")) {
			continue
		}
		samples = append(samples, GpuSample{
			Key:         gpuKey("", value("uuid"), "xpu-smi", index),
			Name:        value("name"),
			State:       GpuStateOK,
			TempC:       parseFloat(value("temperature.gpu")),
			VramTempC:   parseFloat(value("temperature.memory")),
			PowerW:      parseFloat(value("power.draw")),
			UtilPct:     parseFloat(value("utilization.gpu")),
			VramUsedMB:  parseMB(value("memory.used")),
			VramTotalMB: parseMB(value("memory.total")),
			FanPct:      parseFloat(value("fan.speed")),
		})
	}
	if len(samples) == 0 {
		return nil, errors.New("xpu-smi: no GPUs reported")
	}
	return samples, nil
}

func intelPCIKeys() []string {
	var keys []string
	for _, g := range listPCIGPUs(vendorIntel) {
		keys = append(keys, g.Key)
	}
	return keys
}
