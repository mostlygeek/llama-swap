//go:build windows

package perf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePdhLuid_Valid(t *testing.T) {
	name := `pid_25312_luid_0x00000000_0x000148BF_phys_0_eng_2_engtype_Compute`
	got, ok := parsePdhLuid(name)
	assert.True(t, ok)
	assert.Equal(t, uint32(0x000148BF), got.LowPart)
	assert.Equal(t, int32(0x00000000), got.HighPart)
}

func TestParsePdhLuid_ValidNvidia(t *testing.T) {
	name := `pid_1388_luid_0x00000000_0x00011372_phys_0_eng_8_engtype_Compute_1`
	got, ok := parsePdhLuid(name)
	assert.True(t, ok)
	assert.Equal(t, uint32(0x00011372), got.LowPart)
	assert.Equal(t, int32(0x00000000), got.HighPart)
}

func TestParsePdhLuid_NonZeroHighPart(t *testing.T) {
	name := `pid_1234_luid_0x00000001_0x0000C85A_phys_0_eng_5_engtype_Copy`
	got, ok := parsePdhLuid(name)
	assert.True(t, ok)
	assert.Equal(t, uint32(0x0000C85A), got.LowPart)
	assert.Equal(t, int32(0x00000001), got.HighPart)
}

func TestParsePdhLuid_InvalidNoLuid(t *testing.T) {
	_, ok := parsePdhLuid("invalid_string_without_luid")
	assert.False(t, ok)
}

func TestParsePdhLuid_InvalidEmpty(t *testing.T) {
	_, ok := parsePdhLuid("")
	assert.False(t, ok)
}

func TestParsePdhLuid_InvalidHex(t *testing.T) {
	_, ok := parsePdhLuid("pid_1234_luid_0xZZZZ_0xGGGG_phys_0")
	assert.False(t, ok)
}

func TestParsePdhLuid_ShortAfterLuid(t *testing.T) {
	_, ok := parsePdhLuid("pid_1234_luid_0x00000000")
	assert.False(t, ok)
}
