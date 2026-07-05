package config

import (
	"fmt"
	"time"
)

// PerformanceConfig holds configuration for system performance monitoring
type PerformanceConfig struct {
	Disabled bool          `yaml:"disabled"`
	Every    time.Duration `yaml:"every"`

	// GpuBudgetMB is an optional ceiling, in MiB, on total GPU-managed
	// memory (VRAM + GTT, as reported by the perf monitor). When > 0 and the
	// monitor is active, the router evicts least-recently-used models before
	// a swap so the projected footprint stays under budget. 0 (default)
	// disables the gate entirely. See internal/router/memgate.go.
	GpuBudgetMB int `yaml:"gpuBudgetMB"`

	// GpuStrictAdmission upgrades the budget gate from fail-open to
	// fail-closed. When true, a model load that would exceed GpuBudgetMB
	// (after LRU eviction and up to AdmissionMaxWait of waiting for memory to
	// free) is REJECTED with HTTP 503 + Retry-After instead of proceeding.
	// Models without a vramEstimateMB are also rejected, since the gate
	// cannot reason about their footprint. Default false preserves the
	// historical fail-open behavior.
	GpuStrictAdmission bool `yaml:"gpuStrictAdmission"`

	// AdmissionMaxWait bounds how long a strict-mode admission will wait
	// (re-checking every few seconds) for memory to free up — e.g. a TTL
	// unload or another model finishing its swap — before rejecting with
	// 503. Only used when GpuStrictAdmission is true. Default 30s.
	AdmissionMaxWait time.Duration `yaml:"admissionMaxWait"`

	// HostMemFloorMB, when > 0, adds a host-RAM admission check: a load is
	// only admitted when the projected host MemAvailable after the load
	// (current available - vramEstimateMB) stays above this floor. On UMA
	// systems (APUs/iGPUs) GPU memory is carved out of the same physical
	// RAM, so gating GTT alone lets host memory starve; this closes that gap.
	HostMemFloorMB int `yaml:"hostMemFloorMB"`

	// GpuRedlineMB, when > 0, enables the runtime red-line watchdog: a ~1s
	// ticker that reads live GPU memory directly and evicts idle
	// least-recently-used models whenever usage exceeds this value (or host
	// MemAvailable drops below HostMemFloorMB). This catches what admission
	// cannot: estimate drift, post-load growth, and out-of-band allocations.
	// Should be >= GpuBudgetMB.
	GpuRedlineMB int `yaml:"gpuRedlineMB"`

	// GpuHardlineMB, when > 0, is the emergency tier of the red-line
	// watchdog: above this value ALL models — including actively serving
	// ones — are eligible for eviction, oldest first. Losing a model beats
	// the kernel TTM eviction stall / hardware-watchdog reboot that follows
	// unchecked growth. Should be >= GpuRedlineMB.
	GpuHardlineMB int `yaml:"gpuHardlineMB"`

	// SerializeInference, when true, allows at most one upstream inference
	// handler to execute at a time across all local models. Default false
	// preserves historical parallel behaviour.
	SerializeInference bool `yaml:"serializeInference"`
}

func (p *PerformanceConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawPerformanceConfig PerformanceConfig
	defaults := rawPerformanceConfig{
		Every:            5 * time.Second,
		AdmissionMaxWait: 30 * time.Second,
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	*p = PerformanceConfig(defaults)
	return nil
}

// Validate checks the PerformanceConfig values and returns an error if invalid
func (p *PerformanceConfig) Validate() error {
	if p.Every < 5*time.Second {
		return fmt.Errorf("every must be at least 5s, got %v", p.Every)
	}
	if p.GpuStrictAdmission && p.GpuBudgetMB <= 0 {
		return fmt.Errorf("gpuStrictAdmission requires gpuBudgetMB > 0")
	}
	if p.AdmissionMaxWait < 0 {
		return fmt.Errorf("admissionMaxWait must be >= 0, got %v", p.AdmissionMaxWait)
	}
	if p.GpuRedlineMB > 0 && p.GpuBudgetMB > 0 && p.GpuRedlineMB < p.GpuBudgetMB {
		return fmt.Errorf("gpuRedlineMB (%d) must be >= gpuBudgetMB (%d)", p.GpuRedlineMB, p.GpuBudgetMB)
	}
	if p.GpuHardlineMB > 0 {
		if p.GpuRedlineMB <= 0 {
			return fmt.Errorf("gpuHardlineMB requires gpuRedlineMB > 0")
		}
		if p.GpuHardlineMB < p.GpuRedlineMB {
			return fmt.Errorf("gpuHardlineMB (%d) must be >= gpuRedlineMB (%d)", p.GpuHardlineMB, p.GpuRedlineMB)
		}
	}
	return nil
}
