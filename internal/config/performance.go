package config

import (
	"fmt"
	"slices"
	"time"
)

// PerformancePoll sets when the performance monitor takes readings.
type PerformancePoll string

const (
	// PerformancePollAlways reads on every tick.
	PerformancePollAlways PerformancePoll = "always"
	// PerformancePollActive reads only while a model is loaded and someone is
	// watching the stats.
	PerformancePollActive PerformancePoll = "active"
)

// PerformanceProbeAuto selects GPU probes from the detected hardware.
const PerformanceProbeAuto = "auto"

// PerformanceProbeNames are the GPU probe names accepted in
// performance.gpu.probes and performance.gpu.settings.
var PerformanceProbeNames = []string{
	"nvidia-smi",
	"rocm-smi",
	"lact",
	"xpu-smi",
	"sysfs",
	"d3dkmt",
	"mactop",
	"ioreg",
}

// performanceMinEvery is the shortest allowed polling interval.
const performanceMinEvery = 5 * time.Second

// PerformanceConfig holds configuration for system and GPU performance
// monitoring.
type PerformanceConfig struct {
	Disabled bool                    `yaml:"disabled"`
	System   PerformanceSystemConfig `yaml:"system"`
	GPU      PerformanceGPUConfig    `yaml:"gpu"`

	// LegacyEvery catches the removed performance.every key so loading an old
	// config fails with a hint instead of silently ignoring it.
	LegacyEvery time.Duration `yaml:"every"`
}

// PerformanceSystemConfig configures CPU, RAM, swap, load and network stats.
type PerformanceSystemConfig struct {
	Disabled bool            `yaml:"disabled"`
	Every    time.Duration   `yaml:"every"`
	Poll     PerformancePoll `yaml:"poll"`
}

// PerformanceGPUConfig configures GPU stats.
type PerformanceGPUConfig struct {
	Disabled      bool            `yaml:"disabled"`
	Every         time.Duration   `yaml:"every"`
	Poll          PerformancePoll `yaml:"poll"`
	SkipSuspended bool            `yaml:"skipSuspended"`

	// Probes is either ["auto"] or an ordered list of probe names. Earlier
	// probes win when two probes report the same GPU.
	Probes []string `yaml:"probes"`

	// Exclude hides GPUs whose PCI bus ID or UUID equals an entry, or whose
	// name contains one.
	Exclude []string `yaml:"exclude"`

	// Settings holds per-probe options keyed by probe name.
	Settings map[string]PerformanceProbeSettings `yaml:"settings"`
}

// PerformanceProbeSettings holds the options a probe may accept. Path is the
// CLI to run and Socket is a daemon socket.
type PerformanceProbeSettings struct {
	Path   string `yaml:"path"`
	Socket string `yaml:"socket"`
}

// DefaultPerformanceConfig returns the configuration used when the
// performance section, or any of its keys, is missing.
func DefaultPerformanceConfig() PerformanceConfig {
	return PerformanceConfig{
		System: PerformanceSystemConfig{
			Every: performanceMinEvery,
			Poll:  PerformancePollAlways,
		},
		GPU: PerformanceGPUConfig{
			Every:         performanceMinEvery,
			Poll:          PerformancePollActive,
			SkipSuspended: true,
			Probes:        []string{PerformanceProbeAuto},
		},
	}
}

// UsesAutoProbes reports whether GPU probes are picked from detected hardware.
func (g PerformanceGPUConfig) UsesAutoProbes() bool {
	return len(g.Probes) == 0 || (len(g.Probes) == 1 && g.Probes[0] == PerformanceProbeAuto)
}

// Validate checks the PerformanceConfig values and returns an error if invalid
func (p *PerformanceConfig) Validate() error {
	if p.LegacyEvery != 0 {
		return fmt.Errorf("every was removed, use system.every and gpu.every")
	}
	if err := validatePoll("system", p.System.Every, p.System.Poll); err != nil {
		return err
	}
	if err := validatePoll("gpu", p.GPU.Every, p.GPU.Poll); err != nil {
		return err
	}

	seen := make(map[string]bool, len(p.GPU.Probes))
	for _, name := range p.GPU.Probes {
		if name == PerformanceProbeAuto {
			if len(p.GPU.Probes) > 1 {
				return fmt.Errorf("gpu.probes: auto cannot be combined with other probes")
			}
			continue
		}
		if !slices.Contains(PerformanceProbeNames, name) {
			return fmt.Errorf("gpu.probes: unknown probe %q", name)
		}
		if seen[name] {
			return fmt.Errorf("gpu.probes: %q is listed more than once", name)
		}
		seen[name] = true
	}

	for name, s := range p.GPU.Settings {
		if !slices.Contains(PerformanceProbeNames, name) {
			return fmt.Errorf("gpu.settings: unknown probe %q", name)
		}
		if s.Socket != "" && name != "lact" {
			return fmt.Errorf("gpu.settings.%s: socket is only supported by lact", name)
		}
		if s.Path != "" && !probeTakesPath(name) {
			return fmt.Errorf("gpu.settings.%s: path is not supported", name)
		}
	}
	return nil
}

// probeTakesPath reports whether a probe runs a CLI whose location can be set.
func probeTakesPath(name string) bool {
	switch name {
	case "nvidia-smi", "rocm-smi", "xpu-smi", "mactop", "ioreg":
		return true
	}
	return false
}

func validatePoll(section string, every time.Duration, poll PerformancePoll) error {
	if every < performanceMinEvery {
		return fmt.Errorf("%s.every must be at least %s, got %v", section, performanceMinEvery, every)
	}
	switch poll {
	case PerformancePollAlways, PerformancePollActive:
		return nil
	}
	return fmt.Errorf("%s.poll must be %q or %q, got %q", section, PerformancePollAlways, PerformancePollActive, poll)
}
