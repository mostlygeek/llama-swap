package hw

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

var errNvidiaSMINotAvailable = errors.New("nvidia-smi not available")

type nvidiaRecord struct {
	index        int
	name         string
	uuid         string
	busID        string
	architecture string
	memoryBytes  uint64
	driver       string
	powerLimit   float64
}

func detectNvidia(ctx context.Context) ([]detectedAccelerator, error) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil, errNvidiaSMINotAvailable
	}

	const fields = "index,name,uuid,pci.bus_id,memory.total,driver_version,power.limit"
	output, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu="+fields, "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, fmt.Errorf("querying nvidia-smi: %w", err)
	}
	records, err := parseNvidiaCSV(string(output))
	if err != nil {
		return nil, err
	}

	architectureOutput, architectureErr := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=index,architecture", "--format=csv,noheader,nounits").Output()
	if architectureErr == nil {
		applyNvidiaArchitectures(records, string(architectureOutput))
	}
	if hasMissingNvidiaArchitecture(records) {
		computeOutput, computeErr := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=index,compute_cap", "--format=csv,noheader,nounits").Output()
		if computeErr == nil {
			applyNvidiaComputeCapabilities(records, string(computeOutput))
		}
	}

	result := make([]detectedAccelerator, 0, len(records))
	for _, record := range records {
		memory := AcceleratorMemory{Kind: "dedicated"}
		if record.memoryBytes > 0 {
			memory.CapacityBytes = uint64Ptr(record.memoryBytes)
		}
		var driver *Driver
		if version := nonEmptyStringPtr(record.driver); version != nil {
			driver = &Driver{Name: stringPtr("NVIDIA"), Version: version}
		}
		identity := normalizePCIIdentity(record.busID)
		if identity == "" {
			identity = strings.TrimSpace(record.uuid)
		}
		accelerator := Accelerator{
			Kind:         "gpu",
			Vendor:       stringPtr("NVIDIA"),
			Model:        nonEmptyStringPtr(record.name),
			Architecture: nonEmptyStringPtr(record.architecture),
			Memory:       memory,
			Driver:       driver,
		}
		if record.powerLimit > 0 {
			accelerator.PowerLimitWatts = float64Ptr(record.powerLimit)
		}
		result = append(result, detectedAccelerator{identity: identity, value: accelerator})
	}
	return result, nil
}

func parseNvidiaCSV(output string) ([]nvidiaRecord, error) {
	reader := csv.NewReader(strings.NewReader(output))
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing nvidia-smi output: %w", err)
	}
	result := make([]nvidiaRecord, 0, len(rows))
	for _, row := range rows {
		if len(row) != 7 {
			return nil, fmt.Errorf("parsing nvidia-smi output: expected 7 columns, got %d", len(row))
		}
		index, err := strconv.Atoi(strings.TrimSpace(row[0]))
		if err != nil {
			return nil, fmt.Errorf("parsing nvidia-smi index: %w", err)
		}
		memoryMiB, _ := parseOptionalFloat(row[4])
		powerLimit, _ := parseOptionalFloat(row[6])
		result = append(result, nvidiaRecord{
			index:       index,
			name:        strings.TrimSpace(row[1]),
			uuid:        strings.TrimSpace(row[2]),
			busID:       strings.TrimSpace(row[3]),
			memoryBytes: uint64(memoryMiB * 1024 * 1024),
			driver:      strings.TrimSpace(row[5]),
			powerLimit:  powerLimit,
		})
	}
	return result, nil
}

func applyNvidiaArchitectures(records []nvidiaRecord, output string) {
	applyNvidiaIndexedValues(records, output, func(record *nvidiaRecord, value string) {
		record.architecture = value
	})
}

func applyNvidiaComputeCapabilities(records []nvidiaRecord, output string) {
	applyNvidiaIndexedValues(records, output, func(record *nvidiaRecord, value string) {
		if record.architecture == "" {
			record.architecture = nvidiaArchitectureForComputeCapability(value)
		}
	})
}

func applyNvidiaIndexedValues(records []nvidiaRecord, output string, apply func(*nvidiaRecord, string)) {
	reader := csv.NewReader(strings.NewReader(output))
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		return
	}
	byIndex := make(map[int]*nvidiaRecord, len(records))
	for i := range records {
		byIndex[records[i].index] = &records[i]
	}
	for _, row := range rows {
		if len(row) != 2 {
			continue
		}
		index, err := strconv.Atoi(strings.TrimSpace(row[0]))
		record := byIndex[index]
		value := strings.TrimSpace(row[1])
		if err == nil && record != nil && nonEmptyStringPtr(value) != nil {
			apply(record, value)
		}
	}
}

func hasMissingNvidiaArchitecture(records []nvidiaRecord) bool {
	for i := range records {
		if records[i].architecture == "" {
			return true
		}
	}
	return false
}

func nvidiaArchitectureForComputeCapability(value string) string {
	switch strings.TrimSpace(value) {
	case "2.0", "2.1":
		return "Fermi"
	case "3.0", "3.2", "3.5", "3.7":
		return "Kepler"
	case "5.0", "5.2", "5.3":
		return "Maxwell"
	case "6.0", "6.1", "6.2":
		return "Pascal"
	case "7.0", "7.2":
		return "Volta"
	case "7.5":
		return "Turing"
	case "8.0", "8.6", "8.7":
		return "Ampere"
	case "8.9":
		return "Ada Lovelace"
	case "9.0":
		return "Hopper"
	case "10.0", "10.3", "11.0", "12.0", "12.1":
		return "Blackwell"
	default:
		return ""
	}
}

func parseOptionalFloat(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "n/a") || strings.EqualFold(value, "[not supported]") {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	return parsed, err == nil
}

func normalizePCIIdentity(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.EqualFold(value, "n/a") {
		return ""
	}
	parts := strings.Split(value, ":")
	if len(parts) == 2 {
		value = "0000:" + value
		parts = strings.Split(value, ":")
	}
	if len(parts) == 3 {
		if domain, err := strconv.ParseUint(parts[0], 16, 32); err == nil {
			parts[0] = fmt.Sprintf("%04x", domain)
			value = strings.Join(parts, ":")
		}
	}
	return value
}
