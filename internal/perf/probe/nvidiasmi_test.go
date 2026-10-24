package probe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_NvidiaSmiParseLine(t *testing.T) {
	s, ok := parseNvidiaSmiLine("0, NVIDIA GeForce RTX 3080, GPU-1234, 00000000:01:00.0, 65, 80, 8192, 10240, 75, 250.5")
	require.True(t, ok)

	assert.Equal(t, "0000:01:00.0", s.Key)
	assert.Equal(t, "NVIDIA GeForce RTX 3080", s.Name)
	assert.Equal(t, GpuStateOK, s.State)
	assert.Equal(t, 65.0, *s.TempC)
	assert.Equal(t, 80.0, *s.UtilPct)
	assert.Equal(t, 8192, *s.VramUsedMB)
	assert.Equal(t, 10240, *s.VramTotalMB)
	assert.Equal(t, 75.0, *s.FanPct)
	assert.Equal(t, 250.5, *s.PowerW)
	assert.Nil(t, s.SharedUsedMB)
}

func TestProbe_NvidiaSmiParseLineNotAvailable(t *testing.T) {
	// passively cooled cards report no fan, and some report no power
	s, ok := parseNvidiaSmiLine("1, Tesla P40, GPU-5678, 00000000:02:00.0, 40, 0, 100, 24576, [N/A], [N/A]")
	require.True(t, ok)
	assert.Nil(t, s.FanPct)
	assert.Nil(t, s.PowerW)
	assert.Equal(t, 0.0, *s.UtilPct)
}

func TestProbe_NvidiaSmiParseLineTooShort(t *testing.T) {
	_, ok := parseNvidiaSmiLine("0, NVIDIA GPU, GPU-123, 65, 80, 8192, 10240, 75, 250")
	assert.False(t, ok)
	_, ok = parseNvidiaSmiLine("")
	assert.False(t, ok)
}

func TestProbe_NvidiaSmiRead(t *testing.T) {
	withLookPath(t, true)
	run := &fakeRunner{out: map[string]string{
		"--query-gpu=": "0, RTX 4090, GPU-a, 00000000:01:00.0, 50, 10, 1000, 24564, 30, 100\n" +
			"1, RTX 4090, GPU-b, 00000000:02:00.0, 51, 20, 2000, 24564, 31, 110\n",
	}}
	p := newNvidiaSmi(Options{Path: "/opt/nvidia-smi"})
	p.run = run.run

	samples, err := p.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 2)
	assert.Equal(t, "0000:01:00.0", samples[0].Key)
	assert.Equal(t, "0000:02:00.0", samples[1].Key)
	require.Len(t, run.calls, 1)
	assert.Equal(t, "/opt/nvidia-smi", run.calls[0][0])
}

func TestProbe_NvidiaSmiUnifiedMemory(t *testing.T) {
	withLookPath(t, true)
	run := &fakeRunner{out: map[string]string{
		"--query-gpu=":          "0, NVIDIA GB10, GPU-a, 0000000F:01:00.0, 45, 90, [N/A], [N/A], [N/A], 30\n",
		"--query-compute-apps=": "19135\n1000\n",
	}}
	p := newNvidiaSmi(Options{})
	p.run = run.run
	p.systemMemMB = func() *int { return ptr(124610) }

	samples, err := p.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 1)
	s := samples[0]
	assert.Nil(t, s.VramUsedMB)
	assert.Nil(t, s.VramTotalMB)
	assert.Equal(t, 124610, *s.SharedTotalMB)
	assert.Equal(t, 20135, *s.SharedUsedMB)
}

func TestProbe_NvidiaSmiDiscreteWithoutMemoryIsNotUnified(t *testing.T) {
	withLookPath(t, true)
	run := &fakeRunner{out: map[string]string{
		"--query-gpu=": "0, RTX 3090, GPU-a, 00000000:01:00.0, 45, 90, [N/A], [N/A], 30, 300\n",
	}}
	p := newNvidiaSmi(Options{})
	p.run = run.run

	samples, err := p.Read(context.Background())
	require.NoError(t, err)
	assert.Nil(t, samples[0].SharedTotalMB)
	assert.Len(t, run.calls, 1, "compute apps are only queried for unified GPUs")
}
