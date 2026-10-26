// Package probe reads GPU stats from vendor tools and kernel interfaces and
// returns them in one shared shape.
//
// Each probe lives in its own file named after it. Probes that only run a
// cross-platform CLI compile on every OS and return an error when the CLI is
// missing. Probes that use OS APIs, OS paths or an OS-only tool carry a
// _linux, _windows or _darwin filename suffix.
package probe

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// ErrUnavailable is returned (wrapped) when a probe's CLI, socket or kernel
// interface does not exist on this host.
var ErrUnavailable = errors.New("probe unavailable")

// GpuState says whether a GPU was read.
type GpuState string

const (
	GpuStateOK        GpuState = "ok"
	GpuStateSuspended GpuState = "suspended"
)

// GpuSample is one reading of one GPU. A nil field was not reported by the
// probe and must never be shown as 0.
type GpuSample struct {
	// Key identifies the GPU across probes: a PCI bus ID like 0000:03:00.0,
	// else a UUID, else <probe>:<index>. Apple Silicon uses apple:0.
	Key   string
	Name  string
	State GpuState

	UtilPct       *float64
	VramUsedMB    *int
	VramTotalMB   *int
	SharedUsedMB  *int // GTT on AMD, unified memory on GB10 and Apple
	SharedTotalMB *int
	TempC         *float64
	VramTempC     *float64
	FanPct        *float64
	PowerW        *float64
}

// Probe reads GPU stats. The monitor owns the timer and calls Read once per
// tick, so a probe runs no loop or long-lived child process of its own.
type Probe interface {
	Name() string
	// Read takes one reading. It returns an error wrapping ErrUnavailable
	// when the probe's CLI or resource does not exist.
	Read(ctx context.Context) ([]GpuSample, error)
}

// Options configures a probe. Path and Socket come from
// performance.gpu.settings.
type Options struct {
	Path          string
	Socket        string
	SkipSuspended bool
}

// New builds the named probe. It returns an error wrapping ErrUnavailable
// when the probe does not exist on this OS.
func New(name string, opts Options) (Probe, error) {
	if p := newPlatformProbe(name, opts); p != nil {
		return p, nil
	}
	switch name {
	case "nvidia-smi":
		return newNvidiaSmi(opts), nil
	case "rocm-smi":
		return newRocmSmi(opts), nil
	case "xpu-smi":
		return newXpuSmi(opts), nil
	case "mactop":
		return newMactop(opts), nil
	}
	return nil, fmt.Errorf("%s: %w on this OS", name, ErrUnavailable)
}

// cliTimeout bounds a single CLI run.
const cliTimeout = 5 * time.Second

// runner runs a CLI and returns its stdout. Tests replace it.
type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// lookPath reports ErrUnavailable when a probe's CLI cannot be found.
var lookPath = func(file string) (string, error) {
	p, err := exec.LookPath(file)
	if err != nil {
		return "", fmt.Errorf("%s: %w", file, ErrUnavailable)
	}
	return p, nil
}

// cliPath returns the configured path or the default CLI name.
func cliPath(opts Options, def string) string {
	if opts.Path != "" {
		return opts.Path
	}
	return def
}

func ptr[T any](v T) *T { return &v }

const bytesPerMB = 1024 * 1024
