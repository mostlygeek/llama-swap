//go:build unix && !darwin

package perf

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

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
	provider := xpuSmiProvider{runner: fakeXpuSmiRunner{lookPathErr: errors.New("not found")}, logger: newTestLogger()}

	_, err := provider.start(context.Background(), time.Second)
	assert.ErrorIs(t, err, ErrNoGpuTool)
}

func TestXpuSmiProvider_CommandFailure(t *testing.T) {
	provider := xpuSmiProvider{
		runner: fakeXpuSmiRunner{output: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("exit status 1")
		}},
		logger: newTestLogger(),
	}

	_, err := provider.start(context.Background(), time.Second)
	assert.Error(t, err)
}

func TestXpuSmiProvider_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := xpuSmiProvider{
		runner: fakeXpuSmiRunner{output: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			return nil, ctx.Err()
		}},
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
		{name: "first", try: func(context.Context, time.Duration, *logmon.Monitor) (chan []GpuStat, error) {
			called = append(called, "first")
			return nil, ErrNoGpuTool
		}},
		{name: "xpu-smi", try: func(context.Context, time.Duration, *logmon.Monitor) (chan []GpuStat, error) {
			called = append(called, "xpu-smi")
			return expected, nil
		}},
		{name: "last", try: func(context.Context, time.Duration, *logmon.Monitor) (chan []GpuStat, error) {
			called = append(called, "last")
			return nil, ErrNoGpuTool
		}},
	}

	ch, err := getGpuStatsWithProviders(context.Background(), time.Second, newTestLogger(), providers)
	require.NoError(t, err)
	assert.Equal(t, expected, ch)
	assert.Equal(t, []string{"first", "xpu-smi"}, called)
}
