package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPerformanceConfig_Defaults(t *testing.T) {
	content := `
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	// When performance section is missing, defaults should be applied
	assert.True(t, config.Performance.Enable)
	assert.Equal(t, 15*time.Second, config.Performance.Every)
	assert.Equal(t, 1*time.Hour, config.Performance.MaxAge)
	assert.Equal(t, 5*time.Minute, config.Performance.GC)
}

func TestPerformanceConfig_CustomValues(t *testing.T) {
	content := `
performance:
  enable: true
  every: 30s
  maxAge: 12h
  gc: 10m
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	assert.True(t, config.Performance.Enable)
	assert.Equal(t, 30*time.Second, config.Performance.Every)
	assert.Equal(t, 12*time.Hour, config.Performance.MaxAge)
	assert.Equal(t, 10*time.Minute, config.Performance.GC)
}

func TestPerformanceConfig_Disabled(t *testing.T) {
	content := `
performance:
  enable: false
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	assert.False(t, config.Performance.Enable)
	// Duration defaults should still apply
	assert.Equal(t, 15*time.Second, config.Performance.Every)
	assert.Equal(t, 1*time.Hour, config.Performance.MaxAge)
	assert.Equal(t, 5*time.Minute, config.Performance.GC)
}

func TestPerformanceConfig_PartialValues(t *testing.T) {
	content := `
performance:
  every: 10s
  maxAge: 6h
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	// enable should default to true
	assert.True(t, config.Performance.Enable)
	assert.Equal(t, 10*time.Second, config.Performance.Every)
	assert.Equal(t, 6*time.Hour, config.Performance.MaxAge)
	// gc should use default
	assert.Equal(t, 5*time.Minute, config.Performance.GC)
}

func TestPerformanceConfig_InvalidEvery(t *testing.T) {
	content := `
performance:
  every: 500ms
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "every must be at least 1s")
}

func TestPerformanceConfig_InvalidMaxAge(t *testing.T) {
	content := `
performance:
  maxAge: 0s
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "maxAge must be greater than 0")
}

func TestPerformanceConfig_InvalidGC(t *testing.T) {
	content := `
performance:
  gc: 0s
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gc must be greater than 0")
}

func TestPerformanceConfig_ComplexDurations(t *testing.T) {
	content := `
performance:
  every: 1m30s
  maxAge: 2h10m
  gc: 1m
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	assert.Equal(t, 90*time.Second, config.Performance.Every)
	assert.Equal(t, (2*time.Hour)+(10*time.Minute), config.Performance.MaxAge)
	assert.Equal(t, 1*time.Minute, config.Performance.GC)
}
