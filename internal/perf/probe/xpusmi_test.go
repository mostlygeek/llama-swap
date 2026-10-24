package probe

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func TestProbe_XpuSmiParseComplete(t *testing.T) {
	samples, err := parseXpuSmiCSV(readFixture(t, "testdata/xpu-smi/b70-complete.csv"))
	require.NoError(t, err)
	require.Len(t, samples, 1)

	s := samples[0]
	assert.Equal(t, "00000000-0000-0000-0000-000000000001", s.Key)
	assert.Equal(t, "Intel(R) Arc(TM) Pro B70 Graphics", s.Name)
	assert.Equal(t, 57.0, *s.TempC)
	assert.Equal(t, 56.0, *s.VramTempC)
	assert.Equal(t, 140.31, *s.PowerW)
	assert.Equal(t, 22.22, *s.UtilPct)
	assert.Equal(t, 23997, *s.VramUsedMB)
	assert.Equal(t, 32656, *s.VramTotalMB)
	assert.Nil(t, s.FanPct)
}

func TestProbe_XpuSmiParseReorderedColumns(t *testing.T) {
	samples, err := parseXpuSmiCSV(readFixture(t, "testdata/xpu-smi/multiple.csv"))
	require.NoError(t, err)
	require.Len(t, samples, 2)

	assert.Equal(t, "Intel Arc Pro B70", samples[0].Name)
	assert.Equal(t, 57.0, *samples[0].TempC)
	assert.Equal(t, "Intel Arc A770", samples[1].Name)
	assert.Nil(t, samples[1].TempC)
	assert.Nil(t, samples[1].UtilPct)
	assert.Nil(t, samples[1].VramTempC, "column not requested")
}

func TestProbe_XpuSmiParseEmpty(t *testing.T) {
	_, err := parseXpuSmiCSV(nil)
	assert.Error(t, err)
	_, err = parseXpuSmiCSV([]byte("index,name\n"))
	assert.Error(t, err)
}

func TestProbe_XpuSmiReadMatchesPCIByIndex(t *testing.T) {
	withLookPath(t, true)
	run := &fakeRunner{out: map[string]string{"--query-gpu=": string(readFixture(t, "testdata/xpu-smi/multiple.csv"))}}
	p := newXpuSmi(Options{})
	p.run = run.run

	p.pciKeys = func() []string { return []string{"0000:03:00.0", "0000:04:00.0"} }
	samples, err := p.Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "0000:03:00.0", samples[0].Key)
	assert.Equal(t, "0000:04:00.0", samples[1].Key)

	// counts disagree (e.g. an iGPU xpu-smi does not list): keep UUIDs
	p.pciKeys = func() []string { return []string{"0000:00:02.0", "0000:03:00.0", "0000:04:00.0"} }
	samples, err = p.Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "00000000-0000-0000-0000-000000000003", samples[0].Key)
}
