package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadPerformanceConfig(t *testing.T, performance string) (Config, error) {
	t.Helper()
	content := performance + `
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`
	return LoadConfigFromReader(strings.NewReader(content))
}

func TestPerformanceConfig_Defaults(t *testing.T) {
	config, err := loadPerformanceConfig(t, "")
	require.NoError(t, err)

	p := config.Performance
	assert.False(t, p.Disabled)
	assert.False(t, p.System.Disabled)
	assert.Equal(t, 5*time.Second, p.System.Every)
	assert.Equal(t, PerformancePollAlways, p.System.Poll)
	assert.False(t, p.GPU.Disabled)
	assert.Equal(t, 5*time.Second, p.GPU.Every)
	assert.Equal(t, PerformancePollActive, p.GPU.Poll)
	assert.True(t, p.GPU.SkipSuspended)
	assert.Equal(t, []string{"auto"}, p.GPU.Probes)
	assert.True(t, p.GPU.UsesAutoProbes())
}

func TestPerformanceConfig_PartialSectionsKeepDefaults(t *testing.T) {
	config, err := loadPerformanceConfig(t, `
performance:
  system:
    every: 10s
  gpu:
    poll: always
`)
	require.NoError(t, err)

	p := config.Performance
	assert.Equal(t, 10*time.Second, p.System.Every)
	assert.Equal(t, PerformancePollAlways, p.System.Poll)
	assert.Equal(t, 5*time.Second, p.GPU.Every)
	assert.Equal(t, PerformancePollAlways, p.GPU.Poll)
	assert.True(t, p.GPU.SkipSuspended)
	assert.Equal(t, []string{"auto"}, p.GPU.Probes)
}

func TestPerformanceConfig_CustomValues(t *testing.T) {
	config, err := loadPerformanceConfig(t, `
performance:
  disabled: true
  system:
    disabled: true
    every: 1m30s
    poll: active
  gpu:
    disabled: true
    every: 30s
    skipSuspended: false
    probes: [sysfs, rocm-smi]
    exclude: ["Intel UHD", "0000:00:02.0"]
    settings:
      rocm-smi:
        path: /opt/rocm/bin/rocm-smi
      lact:
        socket: /run/lactd.sock
`)
	require.NoError(t, err)

	p := config.Performance
	assert.True(t, p.Disabled)
	assert.True(t, p.System.Disabled)
	assert.Equal(t, 90*time.Second, p.System.Every)
	assert.Equal(t, PerformancePollActive, p.System.Poll)
	assert.True(t, p.GPU.Disabled)
	assert.Equal(t, 30*time.Second, p.GPU.Every)
	assert.False(t, p.GPU.SkipSuspended)
	assert.Equal(t, []string{"sysfs", "rocm-smi"}, p.GPU.Probes)
	assert.False(t, p.GPU.UsesAutoProbes())
	assert.Equal(t, []string{"Intel UHD", "0000:00:02.0"}, p.GPU.Exclude)
	assert.Equal(t, "/opt/rocm/bin/rocm-smi", p.GPU.Settings["rocm-smi"].Path)
	assert.Equal(t, "/run/lactd.sock", p.GPU.Settings["lact"].Socket)
}

func TestPerformanceConfig_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "removed top level every",
			yaml:    "performance:\n  every: 10s",
			wantErr: "every was removed",
		},
		{
			name:    "system every too short",
			yaml:    "performance:\n  system:\n    every: 4s",
			wantErr: "system.every must be at least 5s",
		},
		{
			name:    "gpu every too short",
			yaml:    "performance:\n  gpu:\n    every: 1s",
			wantErr: "gpu.every must be at least 5s",
		},
		{
			name:    "bad poll",
			yaml:    "performance:\n  gpu:\n    poll: sometimes",
			wantErr: `gpu.poll must be "always" or "active"`,
		},
		{
			name:    "unknown probe",
			yaml:    "performance:\n  gpu:\n    probes: [intel_gpu_top]",
			wantErr: `unknown probe "intel_gpu_top"`,
		},
		{
			name:    "auto combined",
			yaml:    "performance:\n  gpu:\n    probes: [auto, sysfs]",
			wantErr: "auto cannot be combined",
		},
		{
			name:    "duplicate probe",
			yaml:    "performance:\n  gpu:\n    probes: [sysfs, sysfs]",
			wantErr: "listed more than once",
		},
		{
			name:    "settings for unknown probe",
			yaml:    "performance:\n  gpu:\n    settings:\n      foo:\n        path: /x",
			wantErr: `gpu.settings: unknown probe "foo"`,
		},
		{
			name:    "socket on a cli probe",
			yaml:    "performance:\n  gpu:\n    settings:\n      nvidia-smi:\n        socket: /x",
			wantErr: "socket is only supported by lact",
		},
		{
			name:    "path on sysfs",
			yaml:    "performance:\n  gpu:\n    settings:\n      sysfs:\n        path: /x",
			wantErr: "gpu.settings.sysfs: path is not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadPerformanceConfig(t, tt.yaml)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
