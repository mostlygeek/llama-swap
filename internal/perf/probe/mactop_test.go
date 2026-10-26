package probe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mactopSample = `{"soc_metrics":{"gpu_power":12.5,"gpu_freq_mhz":1398,"gpu_temp":51.6},` +
	`"memory":{"total":68719476736,"used":34359738368},"gpu_usage":42.0,` +
	`"system_info":{"name":"Apple M4 Max","gpu_core_count":40},` +
	`"fans":[{"rpm":2000,"min_rpm":1000,"max_rpm":5000},{"rpm":3000,"min_rpm":1000,"max_rpm":5000}],` +
	`"temperatures":[{"group":"CPU","avg_celsius":60},{"group":"Memory","avg_celsius":44.4}]}`

func TestProbe_MactopParseLine(t *testing.T) {
	s, ok := parseMactopLine(mactopSample)
	require.True(t, ok)

	assert.Equal(t, AppleKey, s.Key)
	assert.Equal(t, "Apple M4 Max (40-core GPU)", s.Name)
	assert.Equal(t, 42.0, *s.UtilPct)
	assert.Equal(t, 12.5, *s.PowerW)
	assert.Equal(t, 52.0, *s.TempC)
	assert.Equal(t, 44.0, *s.VramTempC)
	assert.Equal(t, 37.5, *s.FanPct)
	assert.Equal(t, 65536, *s.SharedTotalMB)
	assert.Nil(t, s.SharedUsedMB, "mactop memory is whole-system, not GPU")
	assert.Nil(t, s.VramTotalMB)
}

func TestProbe_MactopParseNoFans(t *testing.T) {
	s, ok := parseMactopLine(`{"gpu_usage":1,"system_info":{"name":""}}`)
	require.True(t, ok)
	assert.Nil(t, s.FanPct)
	assert.Equal(t, "Apple GPU", s.Name)
}

func TestProbe_MactopParseInvalid(t *testing.T) {
	_, ok := parseMactopLine("not json")
	assert.False(t, ok)
}

func TestProbe_MactopReadUsesGpuMemory(t *testing.T) {
	withLookPath(t, true)
	run := &fakeRunner{out: map[string]string{"--headless": mactopSample}}
	p := newMactop(Options{})
	p.run = run.run
	p.gpuMemUsedMB = func(context.Context) *int { return ptr(7332) }

	samples, err := p.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 1)
	assert.Equal(t, 7332, *samples[0].SharedUsedMB)
}
