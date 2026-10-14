package perf

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLogger() *logmon.Monitor {
	return logmon.NewWriter(io.Discard)
}

func TestNew_DefaultConfig(t *testing.T) {
	logger := newTestLogger()

	m, err := New(config.PerformanceConfig{}, logger)
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.Equal(t, 100*time.Millisecond, m.conf.Every)
}

func TestNew_CustomConfig(t *testing.T) {
	logger := newTestLogger()

	cfg := config.PerformanceConfig{
		Every: 500 * time.Millisecond,
	}

	m, err := New(cfg, logger)
	require.NoError(t, err)

	assert.Equal(t, 500*time.Millisecond, m.conf.Every)
}

func TestNew_NilLogger(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, nil)
	assert.Error(t, err)
	assert.Nil(t, m)
}

func TestNew_BelowMinimumConfig(t *testing.T) {
	logger := newTestLogger()

	cfg := config.PerformanceConfig{
		Every: 1 * time.Millisecond,
	}

	m, err := New(cfg, logger)
	require.NoError(t, err)

	assert.Equal(t, 100*time.Millisecond, m.conf.Every)
}

func TestSubscribe_ReturnsChannels(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	sysCh, gpuCh, unsub := m.Subscribe()
	defer unsub()

	assert.NotNil(t, sysCh)
	assert.NotNil(t, gpuCh)
	assert.NotNil(t, unsub)
}

func TestSubscribe_UnsubscribeRemovesListeners(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	_, _, unsub := m.Subscribe()

	m.mutex.RLock()
	assert.Len(t, m.sysListeners, 1)
	assert.Len(t, m.gpuListeners, 1)
	m.mutex.RUnlock()

	unsub()

	m.mutex.RLock()
	assert.Len(t, m.sysListeners, 0)
	assert.Len(t, m.gpuListeners, 0)
	m.mutex.RUnlock()
}

func TestSubscribe_MultipleSubscriptions(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	sysCh1, gpuCh1, unsub1 := m.Subscribe()
	sysCh2, gpuCh2, unsub2 := m.Subscribe()
	defer unsub1()
	defer unsub2()

	assert.NotEqual(t, sysCh1, sysCh2)
	assert.NotEqual(t, gpuCh1, gpuCh2)

	m.mutex.RLock()
	assert.Len(t, m.sysListeners, 2)
	assert.Len(t, m.gpuListeners, 2)
	m.mutex.RUnlock()
}

func TestCurrent_EmptyByDefault(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	sysStats, gpuStats := m.Current()
	assert.Empty(t, sysStats)
	assert.Empty(t, gpuStats)
}

func TestCurrent_ReturnsCopies(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	now := time.Now()
	m.sysRing.Push(SysStat{Timestamp: now, MemTotalMB: 1024})
	m.gpuRing.Push([]GpuStat{{Timestamp: now, ID: 0, Name: "gpu0"}})

	sysStats, gpuStats := m.Current()

	assert.Len(t, sysStats, 1)
	assert.Len(t, gpuStats, 1)
	assert.Equal(t, 1024, sysStats[0].MemTotalMB)
	assert.Equal(t, "gpu0", gpuStats[0].Name)

	// modifying the returned slice should not affect the original
	sysStats[0].MemTotalMB = 999
	original, _ := m.Current()
	assert.Equal(t, 1024, original[0].MemTotalMB)
}

func TestStart_CollectsSysStats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	m, err := New(config.PerformanceConfig{Every: 100 * time.Millisecond}, newTestLogger())
	require.NoError(t, err)

	m.Start()

	time.Sleep(350 * time.Millisecond)
	m.Stop()

	sysStats, _ := m.Current()
	assert.NotEmpty(t, sysStats, "expected sys stats to be collected")
}

func TestStart_StopStopsGoroutines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	m, err := New(config.PerformanceConfig{Every: 100 * time.Millisecond}, newTestLogger())
	require.NoError(t, err)

	m.Start()
	if m.stopCancel == nil {
		t.Error("stopCancel should not be nil after Start()")
	}

	m.Stop()
	if m.stopCancel != nil {
		t.Error("stopCancel should be nil after Stop()")
	}
}

func TestStart_SubscriberReceivesStats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}

	m, err := New(config.PerformanceConfig{Every: 100 * time.Millisecond}, newTestLogger())
	require.NoError(t, err)

	sysCh, _, unsub := m.Subscribe()
	defer unsub()

	m.Start()
	defer m.Stop()

	select {
	case s := <-sysCh:
		assert.False(t, s.Timestamp.IsZero())
		assert.NotEmpty(t, s.CpuUtilPerCore)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for sys stats")
	}
}

// fakeGpuCollector stands in for the platform GPU collector. Each call to its
// start method runs the next step: a step either fails to start, or returns a
// channel that closes without a sample, or delivers one sample and then closes
// or stays open until ctx is done. Its wait method records every restart wait
// and returns at once, unless blockWait is set.
type fakeGpuCollector struct {
	mu    sync.Mutex
	calls int
	steps []fakeGpuStep
	waits []time.Duration

	// blockWait makes the first restart wait signal waiting, block until ctx
	// is done, then signal waitDone.
	blockWait bool
	waiting   chan struct{}
	waitDone  chan struct{}
}

type fakeGpuStep struct {
	err      error
	noSample bool
	stayOpen bool
}

func (f *fakeGpuCollector) start(ctx context.Context, _ time.Duration, _ *logmon.Monitor) (chan []GpuStat, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	step := fakeGpuStep{stayOpen: true}
	if call <= len(f.steps) {
		step = f.steps[call-1]
	}
	f.mu.Unlock()

	if step.err != nil {
		return nil, step.err
	}

	ch := make(chan []GpuStat, 1)
	go func() {
		defer close(ch)
		if step.noSample {
			// exits before it reports anything
			return
		}
		// ID carries the call number so a test can tell collectors apart
		select {
		case ch <- []GpuStat{{ID: call}}:
		case <-ctx.Done():
			return
		}
		if step.stayOpen {
			<-ctx.Done()
		}
	}()
	return ch, nil
}

func (f *fakeGpuCollector) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeGpuCollector) wait(ctx context.Context, d time.Duration) bool {
	f.mu.Lock()
	f.waits = append(f.waits, d)
	block := f.blockWait
	f.mu.Unlock()

	if block {
		f.waiting <- struct{}{}
		<-ctx.Done()
		f.waitDone <- struct{}{}
		return false
	}
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

func (f *fakeGpuCollector) waitHistory() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.waits...)
}

func newGpuTestMonitor(t *testing.T, f *fakeGpuCollector) (*Monitor, chan []GpuStat) {
	t.Helper()
	m, err := New(config.PerformanceConfig{Every: 100 * time.Millisecond}, newTestLogger())
	require.NoError(t, err)
	m.gpuStats = f.start
	m.gpuWait = f.wait

	_, gpuCh, unsub := m.Subscribe()
	t.Cleanup(unsub)
	return m, gpuCh
}

func waitGpuStat(t *testing.T, gpuCh chan []GpuStat, wantID int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case g := <-gpuCh:
			if len(g) == 1 && g[0].ID == wantID {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for GPU stats from collector %d", wantID)
		}
	}
}

func TestStart_RestartsGpuCollectorAfterItStops(t *testing.T) {
	// the first collector delivers one sample and exits, like nvidia-smi dying
	f := &fakeGpuCollector{steps: []fakeGpuStep{{stayOpen: false}}}
	m, gpuCh := newGpuTestMonitor(t, f)

	m.Start()
	defer m.Stop()

	waitGpuStat(t, gpuCh, 1)
	waitGpuStat(t, gpuCh, 2)
	assert.Equal(t, 2, f.callCount())
}

func TestStart_RetriesWhenGpuCollectorRestartFails(t *testing.T) {
	f := &fakeGpuCollector{steps: []fakeGpuStep{
		{stayOpen: false},
		{err: ErrNoGpuTool},
		{err: errors.New("nvidia-smi start failed")},
	}}
	m, gpuCh := newGpuTestMonitor(t, f)

	m.Start()
	defer m.Stop()

	waitGpuStat(t, gpuCh, 1)
	waitGpuStat(t, gpuCh, 4)
	assert.Equal(t, 4, f.callCount())
}

func TestStart_GpuCollectorNotRestartedAfterStop(t *testing.T) {
	f := &fakeGpuCollector{}
	m, gpuCh := newGpuTestMonitor(t, f)

	m.Start()
	waitGpuStat(t, gpuCh, 1)
	m.Stop()

	// several restart delays
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, f.callCount())
}

func TestStart_GpuCollectorUnavailableAtStartIsNotRetried(t *testing.T) {
	f := &fakeGpuCollector{steps: []fakeGpuStep{{err: ErrNoGpuTool}}}
	m, _ := newGpuTestMonitor(t, f)

	m.Start()
	defer m.Stop()

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, f.callCount())
	assert.Empty(t, f.waitHistory())
}

func TestStart_GpuRestartWaitDoublesUpToMax(t *testing.T) {
	// one working collector, then restarts that exit without stats or fail
	// to start, then a collector that works and stays up
	f := &fakeGpuCollector{steps: []fakeGpuStep{
		{stayOpen: false},
		{noSample: true},
		{err: errors.New("nvidia-smi start failed")},
		{noSample: true},
		{noSample: true},
	}}
	m, gpuCh := newGpuTestMonitor(t, f)

	m.Start()
	defer m.Stop()

	waitGpuStat(t, gpuCh, 1)
	waitGpuStat(t, gpuCh, 6)
	assert.Equal(t, []time.Duration{
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}, f.waitHistory())
}

func TestStart_GpuRestartWaitResetsAfterStats(t *testing.T) {
	f := &fakeGpuCollector{steps: []fakeGpuStep{
		{stayOpen: false},
		{noSample: true},
		{noSample: true},
		{stayOpen: false}, // delivers stats, so the wait starts over
		{noSample: true},
	}}
	m, gpuCh := newGpuTestMonitor(t, f)

	m.Start()
	defer m.Stop()

	waitGpuStat(t, gpuCh, 1)
	waitGpuStat(t, gpuCh, 4)
	waitGpuStat(t, gpuCh, 6)
	assert.Equal(t, []time.Duration{
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		5 * time.Second,
		10 * time.Second,
	}, f.waitHistory())
}

func TestStart_StopDuringRestartWaitEndsCollector(t *testing.T) {
	f := &fakeGpuCollector{
		steps:     []fakeGpuStep{{stayOpen: false}},
		blockWait: true,
		waiting:   make(chan struct{}, 1),
		waitDone:  make(chan struct{}, 1),
	}
	m, gpuCh := newGpuTestMonitor(t, f)

	m.Start()
	waitGpuStat(t, gpuCh, 1)
	select {
	case <-f.waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the restart wait to begin")
	}

	m.Stop()
	select {
	case <-f.waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("restart wait did not end after Stop")
	}

	// the loop must return instead of starting the collector again
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, f.callCount())
}

func TestWaitOrDone_ReturnsTrueAfterDelay(t *testing.T) {
	assert.True(t, waitOrDone(context.Background(), time.Millisecond))
}

func TestWaitOrDone_ReturnsFalseWhenContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	assert.False(t, waitOrDone(ctx, time.Hour))
	assert.Less(t, time.Since(start), time.Second)
}

func TestReadSysStats(t *testing.T) {
	s, err := ReadSysStats()
	require.NoError(t, err)

	assert.False(t, s.Timestamp.IsZero())
	assert.NotEmpty(t, s.CpuUtilPerCore)
	assert.Greater(t, s.MemTotalMB, 0)
}

func TestCurrent_ConcurrentAccess(t *testing.T) {
	m, err := New(config.PerformanceConfig{}, newTestLogger())
	require.NoError(t, err)

	m.sysRing.Push(SysStat{Timestamp: time.Now(), MemTotalMB: 1024})
	m.gpuRing.Push([]GpuStat{{Timestamp: time.Now(), ID: 0}})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sys, gpu := m.Current()
			assert.Len(t, sys, 1)
			assert.Len(t, gpu, 1)
		}()
	}
	wg.Wait()
}

func TestParseNvidiaSmiLine_ValidLine(t *testing.T) {
	line := "0, NVIDIA GeForce RTX 3080, GPU-12345678-1234-1234-1234-123456789abc, 65, 80, 8192, 10240, 75, 250"

	stat := ParseNvidiaSmiLine(line)
	require.NotNil(t, stat)

	assert.Equal(t, 0, stat.ID)
	assert.Equal(t, "NVIDIA GeForce RTX 3080", stat.Name)
	assert.Equal(t, "GPU-12345678-1234-1234-1234-123456789abc", stat.UUID)
	assert.Equal(t, 65, stat.TempC)
	assert.Equal(t, 80.0, stat.GpuUtilPct)
	assert.Equal(t, 8192, stat.MemUsedMB)
	assert.Equal(t, 10240, stat.MemTotalMB)
	assert.Equal(t, 75.0, stat.FanSpeedPct)
	assert.Equal(t, 250.0, stat.PowerDrawW)
	assert.InDelta(t, 80.0, stat.MemUtilPct, 0.01)
}

func TestParseNvidiaSmiLine_ShortLine(t *testing.T) {
	line := "0, NVIDIA GPU, GPU-123"

	stat := ParseNvidiaSmiLine(line)
	assert.Nil(t, stat)
}

func TestParseNvidiaSmiLine_MissingFields(t *testing.T) {
	line := "0, NVIDIA GPU, GPU-123, 65, 80, 8192, 10240, 75"

	stat := ParseNvidiaSmiLine(line)
	assert.Nil(t, stat)
}

func TestParseNvidiaSmiLine_ZeroMemoryTotal(t *testing.T) {
	line := "0, NVIDIA GPU, GPU-123, 65, 80, 0, 0, 75, 250"

	stat := ParseNvidiaSmiLine(line)
	require.NotNil(t, stat)
	assert.Equal(t, 0.0, stat.MemUtilPct)
}

func TestParseIntelGpuTop_ValidObject(t *testing.T) {
	// "resident" as a string exercises flexFloat64's string path.
	data := `{
		"power": {"GPU": 5.5, "Package": 8.0, "unit": "W"},
		"rc6": {"value": 40.0, "unit": "%"},
		"engines": {
			"Render/3D/0": {"busy": 75.0, "unit": "%"},
			"Video/0": {"busy": 10.0, "unit": "%"}
		},
		"clients": {
			"1": {"name": "llama-server", "pid": "1234",
				"memory": {"local": {"resident": "1073741824"}}}
		}
	}`

	stat := ParseIntelGpuTop(data)
	require.NotNil(t, stat)
	assert.Equal(t, 75.0, stat.GpuUtilPct) // max engine busy, not RC6 fallback
	assert.Equal(t, 5.5, stat.PowerDrawW)  // GPU preferred over Package
	assert.Equal(t, 1024, stat.MemUsedMB)  // 1 GiB resident summed across clients
}

func TestParseIntelGpuTop_RC6Fallback(t *testing.T) {
	// Engines report 0 busy -> util derived from 100 - rc6, power falls back to Package.
	data := `{
		"power": {"GPU": 0.0, "Package": 8.0, "unit": "W"},
		"rc6": {"value": 40.0, "unit": "%"},
		"engines": {"Render/3D/0": {"busy": 0.0, "unit": "%"}}
	}`

	stat := ParseIntelGpuTop(data)
	require.NotNil(t, stat)
	assert.Equal(t, 60.0, stat.GpuUtilPct)
	assert.Equal(t, 8.0, stat.PowerDrawW)
}

func TestParseIntelGpuTop_Invalid(t *testing.T) {
	assert.Nil(t, ParseIntelGpuTop("not json"))
}

const ioregSample = `+-o AGXAcceleratorG13X  <class AGXAcceleratorG13X, id 0x1000009a1, registered, matched, active, busy 0 (39191 ms), retain 108>
    {
      "model" = "Apple M1 Pro"
      "gpu-core-count" = 16
      "PerformanceStatistics" = {"In use system memory (driver)"=0,"Alloc system memory"=14511046656,"Tiler Utilization %"=34,"recoveryCount"=0,"Renderer Utilization %"=34,"Device Utilization %"=34,"In use system memory"=7688503296}
      "IOClass" = "AGXAcceleratorG13X"
    }`

func TestParseIoregOutput_ValidOutput(t *testing.T) {
	const memTotalMB = 32768

	stat := ParseIoregOutput([]byte(ioregSample), memTotalMB)
	require.NotNil(t, stat)

	assert.Equal(t, 0, stat.ID)
	assert.Equal(t, "Apple M1 Pro (16-core GPU)", stat.Name)
	assert.Equal(t, 34.0, stat.GpuUtilPct)
	assert.Equal(t, 7688503296/(1024*1024), stat.MemUsedMB)
	assert.Equal(t, memTotalMB, stat.MemTotalMB)
	assert.InDelta(t, float64(stat.MemUsedMB)/memTotalMB*100, stat.MemUtilPct, 0.01)
	// Not exposed by ioreg.
	assert.Equal(t, 0, stat.TempC)
	assert.Equal(t, 0.0, stat.PowerDrawW)
	assert.Equal(t, 0.0, stat.FanSpeedPct)
}

func TestParseIoregOutput_NoGpuDevice(t *testing.T) {
	stat := ParseIoregOutput([]byte("no gpu here"), 32768)
	assert.Nil(t, stat)
}

func TestParseIoregOutput_ZeroMemTotal(t *testing.T) {
	stat := ParseIoregOutput([]byte(ioregSample), 0)
	require.NotNil(t, stat)
	assert.Equal(t, 0.0, stat.MemUtilPct)
}

func TestParseIoregOutput_MissingModel(t *testing.T) {
	const out = `"Device Utilization %"=50,"In use system memory"=1048576`

	stat := ParseIoregOutput([]byte(out), 1024)
	require.NotNil(t, stat)
	assert.Equal(t, "Apple GPU", stat.Name)
	assert.Equal(t, 50.0, stat.GpuUtilPct)
	assert.Equal(t, 1, stat.MemUsedMB)
}
