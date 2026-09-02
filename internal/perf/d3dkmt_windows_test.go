//go:build windows

package perf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestD3dkmtNodeUtil_FullLoad(t *testing.T) {
	prev := nodeRunningTimes{Global: 1000, System: 10000}
	cur := nodeRunningTimes{Global: 5000, System: 14000}
	got := d3dkmtNodeUtil(prev, cur, 100000)
	assert.Equal(t, 100.0, got)
}

func TestD3dkmtNodeUtil_PartialUtil(t *testing.T) {
	prev := nodeRunningTimes{Global: 1000, System: 10000}
	cur := nodeRunningTimes{Global: 3000, System: 14000}
	got := d3dkmtNodeUtil(prev, cur, 100000)
	assert.Equal(t, 50.0, got)
}

func TestD3dkmtNodeUtil_Identical(t *testing.T) {
	prev := nodeRunningTimes{Global: 10000, System: 10000}
	cur := nodeRunningTimes{Global: 20000, System: 20000}
	got := d3dkmtNodeUtil(prev, cur, 100000)
	assert.Equal(t, 100.0, got)
}

func TestD3dkmtNodeUtil_CounterWrap(t *testing.T) {
	prev := nodeRunningTimes{Global: 9000, System: 10000}
	cur := nodeRunningTimes{Global: 1000, System: 10000}
	got := d3dkmtNodeUtil(prev, cur, 100000)
	assert.Equal(t, -1.0, got)
}

func TestD3dkmtNodeUtil_SystemWrap(t *testing.T) {
	prev := nodeRunningTimes{Global: 1000, System: 9000}
	cur := nodeRunningTimes{Global: 5000, System: 1000}
	got := d3dkmtNodeUtil(prev, cur, 100000)
	assert.Equal(t, -1.0, got)
}

func TestD3dkmtNodeUtil_ZeroDelta(t *testing.T) {
	prev := nodeRunningTimes{Global: 1000, System: 10000}
	cur := nodeRunningTimes{Global: 1000, System: 10000}
	got := d3dkmtNodeUtil(prev, cur, 100000)
	assert.Equal(t, 0.0, got)
}

func TestD3dkmtNodeUtil_ElapsedFallback(t *testing.T) {
	prev := nodeRunningTimes{Global: 1000, System: 10000}
	cur := nodeRunningTimes{Global: 6000, System: 10000}
	got := d3dkmtNodeUtil(prev, cur, 50000)
	assert.InDelta(t, 10.0, got, 0.01)
}

func TestD3dkmtFanPct_Normal(t *testing.T) {
	assert.Equal(t, 50.0, d3dkmtFanPct(1500, 3000))
}

func TestD3dkmtFanPct_MaxFan(t *testing.T) {
	assert.Equal(t, 100.0, d3dkmtFanPct(3000, 3000))
}

func TestD3dkmtFanPct_OverMaxClamped(t *testing.T) {
	assert.Equal(t, 100.0, d3dkmtFanPct(4000, 3000))
}

func TestD3dkmtFanPct_ZeroMaxFan(t *testing.T) {
	assert.Equal(t, 0.0, d3dkmtFanPct(1500, 0))
}

func TestD3dkmtFanPct_ZeroFanRPM(t *testing.T) {
	assert.Equal(t, 0.0, d3dkmtFanPct(0, 3000))
}

func TestD3dkmtFanPct_BothZero(t *testing.T) {
	assert.Equal(t, 0.0, d3dkmtFanPct(0, 0))
}

func TestD3dkmtPowerW(t *testing.T) {
	assert.Equal(t, 250.0, d3dkmtPowerW(2500))
}

func TestD3dkmtPowerW_Zero(t *testing.T) {
	assert.Equal(t, 0.0, d3dkmtPowerW(0))
}

func TestD3dkmtTempC(t *testing.T) {
	assert.Equal(t, 65, d3dkmtTempC(650))
}

func TestD3dkmtTempC_Zero(t *testing.T) {
	assert.Equal(t, 0, d3dkmtTempC(0))
}
