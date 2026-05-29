package perf

import (
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
