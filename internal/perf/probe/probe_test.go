package probe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunner returns canned output keyed by the joined arguments and records
// every call.
type fakeRunner struct {
	out   map[string]string
	calls [][]string
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	key := strings.Join(args, " ")
	for prefix, out := range f.out {
		if strings.HasPrefix(key, prefix) {
			return []byte(out), nil
		}
	}
	return nil, fmt.Errorf("unexpected args: %s", key)
}

// withLookPath makes every CLI look installed, or missing when found is false.
func withLookPath(t *testing.T, found bool) {
	t.Helper()
	old := lookPath
	lookPath = func(file string) (string, error) {
		if !found {
			return "", fmt.Errorf("%s: %w", file, ErrUnavailable)
		}
		return file, nil
	}
	t.Cleanup(func() { lookPath = old })
}

func TestProbe_NewUnknownIsUnavailable(t *testing.T) {
	_, err := New("not-a-probe", Options{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnavailable))
}

func TestProbe_NewCrossPlatformProbes(t *testing.T) {
	for _, name := range []string{"nvidia-smi", "rocm-smi", "xpu-smi", "mactop"} {
		p, err := New(name, Options{})
		require.NoError(t, err, name)
		assert.Equal(t, name, p.Name())
	}
}

func TestProbe_MissingCLIIsUnavailable(t *testing.T) {
	withLookPath(t, false)
	for _, name := range []string{"nvidia-smi", "rocm-smi", "xpu-smi", "mactop"} {
		p, err := New(name, Options{})
		require.NoError(t, err)
		_, err = p.Read(context.Background())
		assert.True(t, errors.Is(err, ErrUnavailable), name)
	}
}

func TestProbe_NormalizePCI(t *testing.T) {
	tests := map[string]string{
		"0000:03:00.0":     "0000:03:00.0",
		"00000000:01:00.0": "0000:01:00.0",
		"03:00.0":          "0000:03:00.0",
		"0000:0A:1F.7":     "0000:0a:1f.7",
		" 0000:65:00.0 ":   "0000:65:00.0",
		"":                 "",
		"card0":            "",
		"0000:03:00":       "",
		"0000:03:20.0":     "",
		"GPU-1234":         "",
	}
	for in, want := range tests {
		assert.Equal(t, want, NormalizePCI(in), in)
	}
}

func TestProbe_GpuKeyFallsBack(t *testing.T) {
	assert.Equal(t, "0000:01:00.0", gpuKey("00000000:01:00.0", "GPU-1", "nvidia-smi", 0))
	assert.Equal(t, "GPU-1", gpuKey("[N/A]", "GPU-1", "nvidia-smi", 0))
	assert.Equal(t, "nvidia-smi:2", gpuKey("", "N/A", "nvidia-smi", 2))
}

func TestProbe_ParseFloatNotAvailable(t *testing.T) {
	for _, v := range []string{"", "N/A", "[N/A]", "[Not Supported]", "unknown", "abc"} {
		assert.Nil(t, parseFloat(v), v)
	}
	require.NotNil(t, parseFloat(" 12.5 "))
	assert.Equal(t, 12.5, *parseFloat(" 12.5 "))
}
