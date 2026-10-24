package probe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ioregSample = `+-o AGXAcceleratorG13X  <class AGXAcceleratorG13X, id 0x1000009a1, registered, matched, active, busy 0 (39191 ms), retain 108>
    {
      "model" = "Apple M1 Pro"
      "gpu-core-count" = 16
      "PerformanceStatistics" = {"In use system memory (driver)"=0,"Alloc system memory"=14511046656,"Tiler Utilization %"=34,"recoveryCount"=0,"Renderer Utilization %"=34,"Device Utilization %"=34,"In use system memory"=7688503296}
      "IOClass" = "AGXAcceleratorG13X"
    }`

func TestProbe_IoregParseOutput(t *testing.T) {
	s, ok := parseIoregOutput([]byte(ioregSample))
	require.True(t, ok)

	assert.Equal(t, AppleKey, s.Key)
	assert.Equal(t, "Apple M1 Pro (16-core GPU)", s.Name)
	assert.Equal(t, 34.0, *s.UtilPct)
	assert.Equal(t, 7688503296/bytesPerMB, *s.SharedUsedMB)
	// not exposed by ioreg
	assert.Nil(t, s.TempC)
	assert.Nil(t, s.PowerW)
	assert.Nil(t, s.FanPct)
}

func TestProbe_IoregParseNoGpu(t *testing.T) {
	_, ok := parseIoregOutput([]byte("no gpu here"))
	assert.False(t, ok)
}

func TestProbe_IoregParseMissingModel(t *testing.T) {
	s, ok := parseIoregOutput([]byte(`"Device Utilization %"=50,"In use system memory"=1048576`))
	require.True(t, ok)
	assert.Equal(t, "Apple GPU", s.Name)
	assert.Equal(t, 1, *s.SharedUsedMB)
}

func TestProbe_IoregReadAddsSystemMemory(t *testing.T) {
	withLookPath(t, true)
	run := &fakeRunner{out: map[string]string{"-r": ioregSample}}
	p := newIoreg(Options{})
	p.run = run.run
	p.systemMemMB = func() *int { return ptr(32768) }

	samples, err := p.Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 32768, *samples[0].SharedTotalMB)
}
