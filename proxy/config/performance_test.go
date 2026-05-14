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
	assert.False(t, config.Performance.Disabled)
	assert.Equal(t, 5*time.Second, config.Performance.Every)
}

func TestPerformanceConfig_CustomValues(t *testing.T) {
	content := `
performance:
  enable: true
  every: 30s
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	assert.False(t, config.Performance.Disabled)
	assert.Equal(t, 30*time.Second, config.Performance.Every)
}

func TestPerformanceConfig_Disabled(t *testing.T) {
	content := `
performance:
  disabled: true
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	assert.True(t, config.Performance.Disabled)
	// Duration defaults should still apply
	assert.Equal(t, 5*time.Second, config.Performance.Every)
}

func TestPerformanceConfig_PartialValues(t *testing.T) {
	content := `
performance:
  every: 10s
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	// enable should default to true
	assert.False(t, config.Performance.Disabled)
	assert.Equal(t, 10*time.Second, config.Performance.Every)
}

func TestPerformanceConfig_InvalidEvery(t *testing.T) {
	content := `
performance:
  every: 4s
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "every must be at least 5s")
}

func TestPerformanceConfig_ComplexDurations(t *testing.T) {
	content := `
performance:
  every: 1m30s
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)

	assert.Equal(t, 90*time.Second, config.Performance.Every)
}
