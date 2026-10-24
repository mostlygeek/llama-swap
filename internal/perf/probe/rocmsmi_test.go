package probe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_RocmSmiParseV5(t *testing.T) {
	// rocm-smi 5.7.0-1 (Ubuntu 24)
	out := "device,GPU ID,Temperature (Sensor edge) (C),Temperature (Sensor junction) (C),Temperature (Sensor memory) (C),Fan speed (level),Fan speed (%),Fan RPM,Average Graphics Package Power (W),GPU use (%),GPU memory use (%),Memory Activity,VRAM Total Memory (B),VRAM Total Used Memory (B),Card series,Card model,Card vendor,Card SKU\n" +
		"card0,0x7551,38.0,38.0,39.0,51,20,1068,15.0,3,0,N/A,34359738368,17179869184,0x1002,0x1002,0x1002,APM123456\n"

	samples := parseRocmSmiCSV(out)
	require.Len(t, samples, 1)
	s := samples[0]
	assert.Equal(t, "rocm-smi:0", s.Key)
	assert.Equal(t, 38.0, *s.TempC)
	assert.Equal(t, 39.0, *s.VramTempC)
	assert.Equal(t, 20.0, *s.FanPct)
	assert.Equal(t, 15.0, *s.PowerW)
	assert.Equal(t, 3.0, *s.UtilPct)
	assert.Equal(t, 16384, *s.VramUsedMB)
	assert.Equal(t, 32768, *s.VramTotalMB)
	assert.Nil(t, s.SharedTotalMB)
}

func TestProbe_RocmSmiParseV7WithBusAndGTT(t *testing.T) {
	// rocm-smi 7.1.1 (Ubuntu 26) with --showbus and --showmeminfo vram gtt
	out := "device,Device Name,Device ID,GUID,PCI Bus,Temperature (Sensor edge) (C),Temperature (Sensor memory) (C),Fan speed (%),Average Graphics Package Power (W),GPU use (%),VRAM Total Memory (B),VRAM Total Used Memory (B),GTT Total Memory (B),GTT Total Used Memory (B),Card Series,GFX Version\n" +
		"card1,Radeon 8060S,0x1586,55555,0000:C3:00.0,45.0,N/A,N/A,60.0,98,536870912,91729920,103079215104,68719476736,Strix Halo,gfx1151\n"

	samples := parseRocmSmiCSV(out)
	require.Len(t, samples, 1)
	s := samples[0]
	assert.Equal(t, "0000:c3:00.0", s.Key)
	assert.Equal(t, "Strix Halo (gfx1151)", s.Name)
	assert.Nil(t, s.VramTempC)
	assert.Nil(t, s.FanPct)
	assert.Equal(t, 512, *s.VramTotalMB)
	assert.Equal(t, 98304, *s.SharedTotalMB)
	assert.Equal(t, 65536, *s.SharedUsedMB)
}

// #1154: an N/A current power column must not hide the average power.
func TestProbe_RocmSmiAveragePowerWhenCurrentIsNA(t *testing.T) {
	out := "device,Device Name,GUID,Average Graphics Package Power (W),GPU use (%),Current Socket Graphics Package Power (W)\n" +
		"card0,Generic GPU,12345,16.0,4,N/A\n"
	samples := parseRocmSmiCSV(out)
	require.Len(t, samples, 1)
	assert.Equal(t, 16.0, *samples[0].PowerW)
	assert.Equal(t, "12345", samples[0].Key)
}

func TestProbe_RocmSmiCurrentPowerWhenAverageIsNA(t *testing.T) {
	out := "device,Average Graphics Package Power (W),Current Socket Graphics Package Power (W)\n" +
		"card0,N/A,21.5\n"
	samples := parseRocmSmiCSV(out)
	require.Len(t, samples, 1)
	assert.Equal(t, 21.5, *samples[0].PowerW)
}

func TestProbe_RocmSmiParseInvalid(t *testing.T) {
	header := "device,GPU use (%)\n"
	assert.Empty(t, parseRocmSmiCSV(""))
	assert.Empty(t, parseRocmSmiCSV("card0,5\n"), "rows before a header are ignored")
	assert.Empty(t, parseRocmSmiCSV(header+"card0\n"), "field count mismatch")
	assert.Empty(t, parseRocmSmiCSV(header+"gpu0,5\n"), "device must be cardN")
}

func TestProbe_RocmSmiRead(t *testing.T) {
	withLookPath(t, true)
	run := &fakeRunner{out: map[string]string{
		"-i": "device,GPU use (%)\ncard0,5\n",
	}}
	p := newRocmSmi(Options{})
	p.run = run.run

	samples, err := p.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 1)
	assert.Equal(t, rocmSmiArgs, run.calls[0][1:])
}
