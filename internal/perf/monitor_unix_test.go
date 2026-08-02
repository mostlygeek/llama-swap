//go:build unix && !darwin

package perf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRocmSmiLine_ValidLine_v5(t *testing.T) {
	// statistics from rocm-smi 5.7.0-1 (Ubuntu 24)
	header := "device,GPU ID,Temperature (Sensor edge) (C),Temperature (Sensor junction) (C),Temperature (Sensor memory) (C),Fan speed (level),Fan speed (%),Fan RPM,Average Graphics Package Power (W),GPU use (%),GPU memory use (%),Memory Activity,VRAM Total Memory (B),VRAM Total Used Memory (B),Card series,Card model,Card vendor,Card SKU"
	line := "card0,0x7551,38.0,38.0,39.0,51,20,1068,15.0,3,0,N/A,34359738368,17179869184,0x1002,0x1002,0x1002,APM123456"

	stat := parseRocmSmiLine(header, line)
	require.NotNil(t, stat)

	assert.Equal(t, 0, stat.ID)
	assert.Equal(t, 38, stat.TempC)
	assert.Equal(t, 39, stat.VramTempC)
	assert.Equal(t, 20.0, stat.FanSpeedPct)
	assert.Equal(t, 15.0, stat.PowerDrawW)
	assert.Equal(t, 3.0, stat.GpuUtilPct)
	assert.Equal(t, 17179869184/(1024*1024), stat.MemUsedMB)
	assert.Equal(t, 34359738368/(1024*1024), stat.MemTotalMB)
	assert.Equal(t, 50.0, stat.MemUtilPct)
}

func TestParseRocmSmiLine_ValidLine_v7(t *testing.T) {
	// statistics from rocm-smi 7.1.1-0ubuntu1 (Ubuntu 26)
	header := "device,Device Name,Device ID,Device Rev,Subsystem ID,GUID,Temperature (Sensor edge) (C),Temperature (Sensor junction) (C),Temperature (Sensor memory) (C),Fan speed (level),Fan speed (%),Fan RPM,Average Graphics Package Power (W),GPU use (%),GPU Memory Allocated (VRAM%),GPU Memory Read/Write Activity (%),Memory Activity,Avg. Memory Bandwidth,VRAM Total Memory (B),VRAM Total Used Memory (B),Card Series,Card Model,Card Vendor,Card SKU,Node ID,GFX Version"
	line := "card0,N/A,0x7551,0xc0,0x1234,55555,38.0,38.0,39.0,51,20,1068,15.0,3,88,0,N/A,0,34359738368,17179869184,N/A,0x7551,0x1002,APM123456,1,gfx1201"

	stat := parseRocmSmiLine(header, line)
	require.NotNil(t, stat)

	assert.Equal(t, 0, stat.ID)
	assert.Equal(t, 38, stat.TempC)
	assert.Equal(t, 39, stat.VramTempC)
	assert.Equal(t, 20.0, stat.FanSpeedPct)
	assert.Equal(t, 15.0, stat.PowerDrawW)
	assert.Equal(t, 3.0, stat.GpuUtilPct)
	assert.Equal(t, 17179869184/(1024*1024), stat.MemUsedMB)
	assert.Equal(t, 34359738368/(1024*1024), stat.MemTotalMB)
	assert.Equal(t, 50.0, stat.MemUtilPct)
}

func TestParseRocmSmiLine_InvalidLines(t *testing.T) {
	header := "device,GPU ID,Temperature (Sensor edge) (C),Temperature (Sensor junction) (C),Temperature (Sensor memory) (C),Fan speed (level),Fan speed (%),Fan RPM,Average Graphics Package Power (W),GPU use (%),GPU memory use (%),Memory Activity,VRAM Total Memory (B),VRAM Total Used Memory (B),Card series,Card model,Card vendor,Card SKU"

	tests := []struct {
		name   string
		header string
		line   string
	}{
		{
			name:   "empty header",
			header: "",
			line:   "card0,0x7551,38.0,38.0,39.0,51,20,1068,15.0,0,0,N/A,34359738368,17179869184,0x1002,0x1002,0x1002,APM123456",
		},
		{
			name:   "empty line",
			header: header,
			line:   "",
		},
		{
			name:   "field count mismatch",
			header: header,
			line:   "card0,0x7551,38.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stat := parseRocmSmiLine(tt.header, tt.line)
			assert.Nil(t, stat)
		})
	}
}
