//go:build windows

package hw

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/host"
	"github.com/yusufpapurcu/wmi"
)

const wmiQueryTimeout = 5 * time.Second

type win32VideoController struct {
	Name                 string
	AdapterCompatibility string
	DriverVersion        string
	PNPDeviceID          string
}

type win32ComputerSystem struct {
	Manufacturer string
	Model        string
}

func detectPlatform(ctx context.Context, _ *HardwareSnapshot) ([]detectedAccelerator, error) {
	var result []detectedAccelerator
	if nvidia, err := detectNvidia(ctx); err == nil {
		result = append(result, nvidia...)
	}
	if dxgi, err := detectDXGI(); err == nil {
		for _, candidate := range dxgi {
			if index := matchingAccelerator(result, stringValue(candidate.value.Vendor), stringValue(candidate.value.Model)); index >= 0 {
				mergeAccelerator(&result[index].value, candidate.value)
				continue
			}
			result = append(result, candidate)
		}
	}

	var controllers []win32VideoController
	if err := queryWMI(ctx, "SELECT Name, AdapterCompatibility, DriverVersion, PNPDeviceID FROM Win32_VideoController", &controllers); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return result, nil
		}
		if len(result) > 0 {
			return result, nil
		}
		return nil, fmt.Errorf("querying Win32_VideoController: %w", err)
	}
	for _, controller := range controllers {
		if isSoftwareDisplayAdapter(controller.Name) {
			continue
		}
		vendor := windowsVendor(controller.AdapterCompatibility, controller.Name, controller.PNPDeviceID)
		if index := matchingAccelerator(result, vendor, controller.Name); index >= 0 {
			if version := nonEmptyStringPtr(controller.DriverVersion); version != nil {
				result[index].value.Driver = &Driver{Name: nonEmptyStringPtr(vendor), Version: version}
			}
			continue
		}
		var driver *Driver
		if version := nonEmptyStringPtr(controller.DriverVersion); version != nil {
			driver = &Driver{Name: nonEmptyStringPtr(vendor), Version: version}
		}
		result = append(result, detectedAccelerator{
			identity: strings.ToLower(strings.TrimSpace(controller.PNPDeviceID)),
			value: Accelerator{
				Kind:   "gpu",
				Vendor: nonEmptyStringPtr(vendor),
				Model:  nonEmptyStringPtr(controller.Name),
				Memory: AcceleratorMemory{Kind: "unknown"},
				Driver: driver,
			},
		})
	}
	return result, nil
}

func detectEnvironment(ctx context.Context, _ *host.InfoStat) ExecutionEnvironment {
	var systems []win32ComputerSystem
	if err := queryWMI(ctx, "SELECT Manufacturer, Model FROM Win32_ComputerSystem", &systems); err != nil || len(systems) == 0 {
		return ExecutionEnvironment{Kind: "unknown"}
	}
	combined := strings.ToLower(systems[0].Manufacturer + " " + systems[0].Model)
	for needle, name := range map[string]string{
		"virtual machine": "Hyper-V",
		"vmware":          "VMware",
		"virtualbox":      "VirtualBox",
		"kvm":             "KVM",
		"qemu":            "QEMU",
	} {
		if strings.Contains(combined, needle) {
			return ExecutionEnvironment{Kind: "virtual_machine", Name: stringPtr(name)}
		}
	}
	return ExecutionEnvironment{Kind: "unknown"}
}

func queryWMI(ctx context.Context, query string, destination any) error {
	ctx, cancel := context.WithTimeout(ctx, wmiQueryTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- wmi.Query(query, destination)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func platformMemoryCapacity(total uint64) uint64 { return total }

func isSoftwareDisplayAdapter(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "microsoft basic display") ||
		strings.Contains(name, "remote display") ||
		strings.Contains(name, "indirect display")
}

func windowsVendor(compatibility, name, pnpID string) string {
	combined := strings.ToLower(compatibility + " " + name + " " + pnpID)
	switch {
	case strings.Contains(combined, "ven_10de"), strings.Contains(combined, "nvidia"):
		return "NVIDIA"
	case strings.Contains(combined, "ven_1002"), strings.Contains(combined, "advanced micro devices"), strings.Contains(combined, " amd"), strings.HasPrefix(combined, "amd"):
		return "AMD"
	case strings.Contains(combined, "ven_8086"), strings.Contains(combined, "intel"):
		return "Intel"
	default:
		return strings.TrimSpace(compatibility)
	}
}

func matchingAccelerator(found []detectedAccelerator, vendor, model string) int {
	for i, accelerator := range found {
		if strings.EqualFold(stringValue(accelerator.value.Vendor), vendor) && strings.EqualFold(stringValue(accelerator.value.Model), model) {
			return i
		}
	}
	return -1
}
