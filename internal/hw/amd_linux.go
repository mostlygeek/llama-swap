//go:build linux

package hw

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func detectAMD(ctx context.Context) []detectedAccelerator {
	var result []detectedAccelerator
	if rocm, err := detectROCm(ctx); err == nil {
		result = append(result, rocm...)
	}
	if kfd, err := detectAMDKFD(
		"/sys/class/kfd/kfd/topology/nodes",
		"/sys/class/drm",
		"/dev/dri",
	); err == nil {
		result = append(result, kfd...)
	}
	return result
}

func detectROCm(ctx context.Context) ([]detectedAccelerator, error) {
	if _, err := exec.LookPath("rocm-smi"); err != nil {
		return nil, err
	}
	args := []string{"-i", "--showmeminfo", "vram", "--showproductname", "--showdriverversion", "--showmaxpower", "--showbus", "--csv"}
	output, err := exec.CommandContext(ctx, "rocm-smi", args...).Output()
	if err != nil {
		fallback := []string{"-i", "--showmeminfo", "vram", "--showproductname", "--showbus", "--csv"}
		output, err = exec.CommandContext(ctx, "rocm-smi", fallback...).Output()
	}
	if err != nil {
		return nil, fmt.Errorf("querying rocm-smi: %w", err)
	}
	return parseROCmCSV(string(output))
}

func parseROCmCSV(output string) ([]detectedAccelerator, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var header []string
	var result []detectedAccelerator
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
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
		if len(header) == 0 || len(row) != len(header) {
			continue
		}
		fields := make(map[string]string, len(row))
		for i := range row {
			fields[strings.ToLower(strings.TrimSpace(header[i]))] = strings.TrimSpace(row[i])
		}
		model := firstROCmField(fields, "card series", "device name", "card model")
		architecture := firstROCmField(fields, "gfx version")
		identity := normalizePCIIdentity(firstROCmField(fields, "pci bus", "pci bus id", "bus"))
		if identity == "" {
			identity = firstROCmField(fields, "guid", "unique id")
		}
		memoryBytes, _ := strconv.ParseUint(firstROCmField(fields, "vram total memory (b)"), 10, 64)
		memory := AcceleratorMemory{Kind: "dedicated"}
		if memoryBytes > 0 {
			memory.CapacityBytes = uint64Ptr(memoryBytes)
		}
		driverVersion := firstROCmField(fields, "driver version")
		var driver *Driver
		if version := nonEmptyStringPtr(driverVersion); version != nil {
			driver = &Driver{Name: stringPtr("amdgpu"), Version: version}
		}
		power, _ := parseOptionalFloat(firstROCmField(fields, "max graphics package power (w)", "maximum graphics package power (w)"))
		accelerator := Accelerator{
			Kind:         "gpu",
			Vendor:       stringPtr("AMD"),
			Model:        nonEmptyStringPtr(model),
			Architecture: nonEmptyStringPtr(architecture),
			Memory:       memory,
			Driver:       driver,
		}
		if power > 0 {
			accelerator.PowerLimitWatts = float64Ptr(power)
		}
		result = append(result, detectedAccelerator{identity: identity, value: accelerator})
	}
	return result, scanner.Err()
}

func firstROCmField(fields map[string]string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(fields[name]); value != "" && !strings.EqualFold(value, "n/a") {
			return value
		}
	}
	return ""
}

func detectAMDKFD(nodesRoot, drmRoot, deviceRoot string) ([]detectedAccelerator, error) {
	propertyPaths, err := filepath.Glob(filepath.Join(nodesRoot, "*", "properties"))
	if err != nil {
		return nil, err
	}

	var result []detectedAccelerator
	for _, propertyPath := range propertyPaths {
		properties, err := readKFDProperties(propertyPath)
		if err != nil {
			continue
		}
		architecture := formatGFXTarget(properties["gfx_target_version"])
		renderMinor := properties["drm_render_minor"]
		if architecture == "" || renderMinor == 0 {
			continue
		}

		renderNode := fmt.Sprintf("renderD%d", renderMinor)
		if _, err := os.Stat(filepath.Join(deviceRoot, renderNode)); err != nil {
			continue
		}
		devicePath, err := filepath.EvalSymlinks(filepath.Join(drmRoot, renderNode, "device"))
		if err != nil || pciVendorName(readTrimmed(filepath.Join(devicePath, "vendor"))) != "AMD" {
			continue
		}
		identity := normalizePCIIdentity(filepath.Base(devicePath))
		if identity == "" {
			continue
		}

		result = append(result, detectedAccelerator{
			identity: identity,
			value: Accelerator{
				Kind:         "gpu",
				Vendor:       stringPtr("AMD"),
				Architecture: stringPtr(architecture),
				Memory:       AcceleratorMemory{Kind: "unknown"},
			},
		})
	}
	return result, nil
}

func readKFDProperties(path string) (map[string]uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	properties := make(map[string]uint64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			properties[fields[0]] = value
		}
	}
	return properties, scanner.Err()
}

func formatGFXTarget(version uint64) string {
	if version < 10000 {
		return ""
	}
	major := version / 10000
	minor := (version / 100) % 100
	stepping := version % 100
	if major == 0 || minor > 9 || stepping > 15 {
		return ""
	}
	return fmt.Sprintf("gfx%d%d%x", major, minor, stepping)
}
