package perf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/hw"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/perf/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProbe returns canned samples, or err, and counts reads.
type fakeProbe struct {
	name string

	mu      sync.Mutex
	samples []probe.GpuSample
	err     error
	reads   int
}

func (f *fakeProbe) Name() string { return f.name }

func (f *fakeProbe) Read(context.Context) ([]probe.GpuSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.samples, f.err
}

func (f *fakeProbe) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func ptr[T any](v T) *T { return &v }

func sample(key, name string, util float64) probe.GpuSample {
	return probe.GpuSample{Key: key, Name: name, State: probe.GpuStateOK, UtilPct: &util}
}

// testClock is a manually advanced clock.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newTestMonitor builds a monitor whose probes come from fakes and whose
// clock is controlled by the test.
func newTestMonitor(t *testing.T, mutate func(*config.PerformanceConfig), fakes ...*fakeProbe) (*Monitor, *testClock) {
	t.Helper()
	c := config.DefaultPerformanceConfig()
	names := make([]string, 0, len(fakes))
	for _, f := range fakes {
		names = append(names, f.name)
	}
	c.GPU.Probes = names
	if mutate != nil {
		mutate(&c)
	}

	byName := map[string]*fakeProbe{}
	for _, f := range fakes {
		byName[f.name] = f
	}
	clock := &testClock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	m, err := New(config.DefaultPerformanceConfig(), logmon.NewWriter(io.Discard), nil)
	require.NoError(t, err)
	m.now = clock.now
	m.goos = "linux"
	m.readSys = func() (SysStat, error) { return SysStat{Timestamp: clock.now(), MemTotalMB: 1024}, nil }
	m.newProbe = func(name string, _ probe.Options) (probe.Probe, error) {
		if f, ok := byName[name]; ok {
			return f, nil
		}
		return nil, fmt.Errorf("%s: %w", name, probe.ErrUnavailable)
	}
	m.applyConfig(c)
	t.Cleanup(m.Stop)
	return m, clock
}

func TestPerf_NewNeedsLogger(t *testing.T) {
	_, err := New(config.DefaultPerformanceConfig(), nil, nil)
	assert.Error(t, err)
}

func TestPerf_HigherPriorityProbeWins(t *testing.T) {
	first := &fakeProbe{name: "nvidia-smi", samples: []probe.GpuSample{sample("0000:01:00.0", "RTX", 90)}}
	second := &fakeProbe{name: "sysfs", samples: []probe.GpuSample{
		sample("0000:01:00.0", "NVIDIA nvidia 0x2684", 10),
		sample("0000:00:02.0", "Intel i915", 5),
	}}
	m, _ := newTestMonitor(t, nil, first, second)

	m.readGPU(context.Background())
	_, gpu := m.Latest(context.Background())
	require.NotNil(t, gpu)
	require.Len(t, gpu.GPUs, 2)
	assert.Equal(t, "RTX", gpu.GPUs[0].Name)
	assert.Equal(t, 90.0, *gpu.GPUs[0].UtilPct)
	assert.Equal(t, "0000:00:02.0", gpu.GPUs[1].Key)
}

func TestPerf_FailedProbeRetriedWhenPollingResumes(t *testing.T) {
	broken := &fakeProbe{name: "rocm-smi", err: errors.New("boom")}
	ok := &fakeProbe{name: "sysfs", samples: []probe.GpuSample{sample("0000:03:00.0", "AMD", 1)}}
	m, clock := newTestMonitor(t, nil, broken, ok)

	m.readGPU(context.Background())
	clock.add(5 * time.Second)
	m.readGPU(context.Background())
	assert.Equal(t, 1, broken.readCount(), "a failed probe is skipped while polling continues")
	assert.Equal(t, 2, ok.readCount())

	// an idle gap longer than two intervals counts as resuming
	clock.add(time.Minute)
	m.readGPU(context.Background())
	assert.Equal(t, 2, broken.readCount())
}

func TestPerf_AutoFallsBackWhenEveryPickedProbeFails(t *testing.T) {
	// auto on an NVIDIA-only host picks nvidia-smi; in a container without
	// it, every probe in the full list is tried
	nvidia := &fakeProbe{name: "nvidia-smi", err: fmt.Errorf("x: %w", probe.ErrUnavailable)}
	sysfs := &fakeProbe{name: "sysfs", samples: []probe.GpuSample{sample("0000:01:00.0", "NVIDIA", 3)}}
	m, _ := newTestMonitor(t, nil, nvidia, sysfs)
	m.hardware = &hw.HardwareSnapshot{Accelerators: []hw.Accelerator{
		{Kind: "gpu", Vendor: ptr("NVIDIA"), Memory: hw.AcceleratorMemory{Kind: "dedicated"}},
	}}
	c := m.conf
	c.GPU.Probes = []string{"auto"}
	m.applyConfig(c)
	require.Len(t, m.probes, 1)

	m.readGPU(context.Background())
	_, gpu := m.Latest(context.Background())
	require.NotNil(t, gpu)
	require.Len(t, gpu.GPUs, 1)
	assert.Equal(t, 1, sysfs.readCount())
	assert.Equal(t, 1, nvidia.readCount(), "a failed probe is not retried in the same read")
}

func TestPerf_ManualProbesDoNotFallBack(t *testing.T) {
	nvidia := &fakeProbe{name: "nvidia-smi", err: errors.New("boom")}
	sysfs := &fakeProbe{name: "sysfs"}
	m, _ := newTestMonitor(t, nil, nvidia)
	m.fallback = []probe.Probe{sysfs}

	m.readGPU(context.Background())
	assert.Equal(t, 0, sysfs.readCount())
}

func TestPerf_Exclude(t *testing.T) {
	p := &fakeProbe{name: "sysfs", samples: []probe.GpuSample{
		sample("0000:00:02.0", "Intel UHD 630", 1),
		sample("0000:01:00.0", "RTX 4090", 2),
		sample("GPU-abc", "RTX 3090", 3),
	}}
	m, _ := newTestMonitor(t, func(c *config.PerformanceConfig) {
		c.GPU.Exclude = []string{"Intel UHD", "GPU-abc"}
	}, p)

	m.readGPU(context.Background())
	_, gpu := m.Latest(context.Background())
	require.Len(t, gpu.GPUs, 1)
	assert.Equal(t, "RTX 4090", gpu.GPUs[0].Name)
}

func TestPerf_SuspendedGPUKeepsKnownName(t *testing.T) {
	p := &fakeProbe{name: "nvidia-smi", samples: []probe.GpuSample{sample("0000:01:00.0", "RTX 5090 Laptop", 5)}}
	m, clock := newTestMonitor(t, nil, p)
	m.readGPU(context.Background())

	p.samples = []probe.GpuSample{{Key: "0000:01:00.0", Name: "NVIDIA GPU", State: probe.GpuStateSuspended}}
	clock.add(5 * time.Second)
	m.readGPU(context.Background())

	_, gpu := m.Latest(context.Background())
	require.Len(t, gpu.GPUs, 1)
	assert.Equal(t, "RTX 5090 Laptop", gpu.GPUs[0].Name)
	assert.Equal(t, "RTX 5090 Laptop", m.GPUName("0000:01:00.0"))
	assert.Equal(t, []string{"0000:01:00.0"}, m.cloneStatus().Suspended)
}

func TestPerf_WatchSendsMetaAndReadings(t *testing.T) {
	p := &fakeProbe{name: "nvidia-smi", samples: []probe.GpuSample{sample("0000:01:00.0", "RTX", 5)}}
	m, _ := newTestMonitor(t, nil, p)
	m.SetModelsLoaded(func() bool { return true })

	state, events, stop := m.Watch()
	defer stop()
	assert.Empty(t, state.GPUs)
	assert.Equal(t, 5*time.Second, state.GPUEvery)

	m.readGPU(context.Background())
	kinds := map[EventKind]Event{}
	for len(kinds) < 3 {
		select {
		case e := <-events:
			kinds[e.Kind] = e
		case <-time.After(time.Second):
			t.Fatalf("missing events, got %v", kinds)
		}
	}
	assert.Equal(t, []GpuInfo{{Key: "0000:01:00.0", Name: "RTX"}}, kinds[EventMeta].GPUs)
	assert.Equal(t, StateOK, kinds[EventStatus].Status.GPU)
	assert.Len(t, kinds[EventGPU].GPU.GPUs, 1)
}

func TestPerf_PollActiveNeedsViewerAndLoadedModel(t *testing.T) {
	p := &fakeProbe{name: "nvidia-smi", samples: []probe.GpuSample{sample("0000:01:00.0", "RTX", 5)}}
	m, _ := newTestMonitor(t, nil, p)
	loaded := false
	var mu sync.Mutex
	m.SetModelsLoaded(func() bool { mu.Lock(); defer mu.Unlock(); return loaded })

	m.Start()
	// system stats default to poll: always and read right away
	require.Eventually(t, func() bool { s, _ := m.Latest(context.Background()); return s != nil }, time.Second, 10*time.Millisecond)
	assert.Equal(t, 0, p.readCount(), "poll: active GPUs wait for a viewer")

	_, _, stop := m.Watch()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, p.readCount(), "a viewer alone is not enough without a loaded model")
	stop()

	mu.Lock()
	loaded = true
	mu.Unlock()
	_, _, stop = m.Watch()
	defer stop()
	require.Eventually(t, func() bool { return p.readCount() == 1 }, time.Second, 10*time.Millisecond, "a new viewer wakes the GPU loop")
}

func TestPerf_LatestReadsOnDemandUnderPollActive(t *testing.T) {
	p := &fakeProbe{name: "nvidia-smi", samples: []probe.GpuSample{sample("0000:01:00.0", "RTX", 5)}}
	m, clock := newTestMonitor(t, nil, p)
	m.Start()
	m.SetModelsLoaded(func() bool { return false })

	_, gpu := m.Latest(context.Background())
	assert.Nil(t, gpu, "no model loaded: GPUs are not sampled")
	assert.Equal(t, 0, p.readCount())

	m.SetModelsLoaded(func() bool { return true })
	_, gpu = m.Latest(context.Background())
	require.NotNil(t, gpu)
	assert.Equal(t, 1, p.readCount())

	// a second scrape within the interval reuses the reading
	_, _ = m.Latest(context.Background())
	assert.Equal(t, 1, p.readCount())

	// after the model unloads the old reading goes stale and is dropped
	m.SetModelsLoaded(func() bool { return false })
	clock.add(11 * time.Second)
	_, gpu = m.Latest(context.Background())
	assert.Nil(t, gpu)
}

func TestPerf_HistoryKeepsLastHour(t *testing.T) {
	p := &fakeProbe{name: "nvidia-smi", samples: []probe.GpuSample{sample("0000:01:00.0", "RTX", 5)}}
	m, clock := newTestMonitor(t, nil, p)

	m.readGPU(context.Background())
	clock.add(50 * time.Minute)
	m.readGPU(context.Background())
	clock.add(20 * time.Minute)

	state, _, stop := m.Watch()
	defer stop()
	require.Len(t, state.GPU, 1, "readings older than an hour are dropped")
}

func TestPerf_DisabledSections(t *testing.T) {
	m, _ := newTestMonitor(t, func(c *config.PerformanceConfig) { c.GPU.Disabled = true })
	state, _, stop := m.Watch()
	defer stop()
	assert.Equal(t, time.Duration(0), state.GPUEvery)
	assert.Equal(t, StateDisabled, state.Status.GPU)
	assert.Equal(t, StatePaused, state.Status.System)

	m.UpdateConfig(config.PerformanceConfig{Disabled: true})
	assert.False(t, m.Enabled())
}

func TestPerf_UpdateConfigRebuildsProbes(t *testing.T) {
	a := &fakeProbe{name: "nvidia-smi", samples: []probe.GpuSample{sample("0000:01:00.0", "A", 1)}}
	b := &fakeProbe{name: "sysfs", samples: []probe.GpuSample{sample("0000:02:00.0", "B", 1)}}
	m, _ := newTestMonitor(t, nil, a, b)
	c := m.conf
	c.GPU.Probes = []string{"sysfs"}
	m.UpdateConfig(c)

	m.readGPU(context.Background())
	_, gpu := m.Latest(context.Background())
	require.Len(t, gpu.GPUs, 1)
	assert.Equal(t, "B", gpu.GPUs[0].Name)
}
