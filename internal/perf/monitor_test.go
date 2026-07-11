package perf

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeXpuSmiRunner struct {
	lookPathErr error
	output      func(context.Context, string, ...string) ([]byte, error)
}

func (r fakeXpuSmiRunner) LookPath(string) (string, error) {
	if r.lookPathErr != nil {
		return "", r.lookPathErr
	}
	return "/test/xpu-smi", nil
}

func (r fakeXpuSmiRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.output(ctx, name, args...)
}

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

func TestParseXpuSmiCSV_ArcProB70Complete(t *testing.T) {
	out, err := os.ReadFile("testdata/xpu-smi-b70-complete.csv")
	require.NoError(t, err)

	stats, err := ParseXpuSmiCSV(out)
	require.NoError(t, err)
	require.Len(t, stats, 1)

	stat := stats[0]
	assert.Equal(t, 0, stat.ID)
	assert.Equal(t, "Intel(R) Arc(TM) Pro B70 Graphics", stat.Name)
	assert.Equal(t, 57, stat.TempC)
	assert.Equal(t, 56, stat.VramTempC)
	assert.Equal(t, 22.22, stat.GpuUtilPct)
	assert.Equal(t, 23997, stat.MemUsedMB)
	assert.Equal(t, 32656, stat.MemTotalMB)
	assert.InDelta(t, 73.48, stat.MemUtilPct, 0.01)
	assert.Equal(t, 140.31, stat.PowerDrawW)
	assert.Equal(t, 0.0, stat.FanSpeedPct)
	require.NotNil(t, stat.ComputeUtilPct)
	assert.Equal(t, 99.99, *stat.ComputeUtilPct)
	require.NotNil(t, stat.CopyUtilPct)
	assert.Equal(t, 99.99, *stat.CopyUtilPct)
	require.NotNil(t, stat.PcieRxMBps)
	assert.InDelta(t, 71.808, *stat.PcieRxMBps, 0.001)
}

func TestParseXpuSmiCSV_MultipleGPUsAndMissingMetrics(t *testing.T) {
	out, err := os.ReadFile("testdata/xpu-smi-multiple.csv")
	require.NoError(t, err)

	stats, err := ParseXpuSmiCSV(out)
	require.NoError(t, err)
	require.Len(t, stats, 2)

	assert.Equal(t, 3, stats[0].ID)
	assert.Equal(t, "Intel Arc Pro B70", stats[0].Name)
	require.NotNil(t, stats[0].ComputeUtilPct)
	assert.Equal(t, 99.99, *stats[0].ComputeUtilPct)
	assert.Equal(t, 9, stats[1].ID)
	assert.Equal(t, "Intel Arc A770", stats[1].Name)
	assert.Equal(t, 0.0, stats[1].GpuUtilPct)
	assert.Nil(t, stats[1].ComputeUtilPct)
	assert.Equal(t, 0, stats[1].TempC)
}

func TestParseXpuSmiCSV_MalformedOutput(t *testing.T) {
	_, err := ParseXpuSmiCSV([]byte("index,name,memory.used\nnot-an-index,Intel GPU,1024\n"))
	assert.Error(t, err)

	_, err = ParseXpuSmiCSV([]byte(""))
	assert.Error(t, err)

	_, err = ParseXpuSmiCSV([]byte("index,memory.used\n0,1024\n"))
	assert.Error(t, err)
}

func TestXpuSmiProvider_CommandUnavailable(t *testing.T) {
	provider := xpuSmiProvider{
		runner: fakeXpuSmiRunner{lookPathErr: errors.New("not found")},
		logger: newTestLogger(),
	}

	_, err := provider.start(context.Background(), time.Second)
	assert.ErrorIs(t, err, ErrNoGpuTool)
}

func TestXpuSmiProvider_CommandFailure(t *testing.T) {
	provider := xpuSmiProvider{
		runner: fakeXpuSmiRunner{
			output: func(context.Context, string, ...string) ([]byte, error) {
				return nil, errors.New("exit status 1")
			},
		},
		logger: newTestLogger(),
	}

	_, err := provider.start(context.Background(), time.Second)
	assert.Error(t, err)
}

func TestXpuSmiProvider_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := xpuSmiProvider{
		runner: fakeXpuSmiRunner{
			output: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
				return nil, ctx.Err()
			},
		},
		logger: newTestLogger(),
	}

	_, err := provider.start(ctx, time.Second)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestParseXpuSmiCSV_StableIdentity(t *testing.T) {
	out, err := os.ReadFile("testdata/xpu-smi-b70-complete.csv")
	require.NoError(t, err)

	first, err := ParseXpuSmiCSV(out)
	require.NoError(t, err)
	second, err := ParseXpuSmiCSV(out)
	require.NoError(t, err)

	require.Len(t, first, 1)
	require.Len(t, second, 1)
	assert.Equal(t, first[0].ID, second[0].ID)
	assert.Equal(t, first[0].UUID, second[0].UUID)
}

func TestGetGpuStatsWithProviders_FallsBackToNextProvider(t *testing.T) {
	called := make([]string, 0)
	expected := make(chan []GpuStat)
	providers := []gpuStatsProvider{
		{
			name: "first",
			try: func(context.Context, time.Duration, *logmon.Monitor) (chan []GpuStat, error) {
				called = append(called, "first")
				return nil, ErrNoGpuTool
			},
		},
		{
			name: "xpu-smi",
			try: func(context.Context, time.Duration, *logmon.Monitor) (chan []GpuStat, error) {
				called = append(called, "xpu-smi")
				return expected, nil
			},
		},
		{
			name: "last",
			try: func(context.Context, time.Duration, *logmon.Monitor) (chan []GpuStat, error) {
				called = append(called, "last")
				return nil, ErrNoGpuTool
			},
		},
	}

	ch, err := getGpuStatsWithProviders(context.Background(), time.Second, newTestLogger(), providers)
	require.NoError(t, err)
	assert.Equal(t, expected, ch)
	assert.Equal(t, []string{"first", "xpu-smi"}, called)
}
