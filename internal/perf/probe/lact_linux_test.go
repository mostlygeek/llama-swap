package probe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLactd serves canned responses on a unix socket and returns its path.
func fakeLactd(t *testing.T, responses map[string]string) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "lactd.sock")
	ln, err := net.Listen("unix", socket)
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadBytes('\n')
					if err != nil {
						return
					}
					var req struct {
						Command string            `json:"command"`
						Args    map[string]string `json:"args"`
					}
					if json.Unmarshal(line, &req) != nil {
						return
					}
					resp, ok := responses[req.Command+":"+req.Args["id"]]
					if !ok {
						resp = `{"status":"error","data":"unknown"}`
					}
					conn.Write([]byte(resp + "\n"))
				}
			}(conn)
		}
	}()
	return socket
}

const lactDevices = `{"status":"ok","data":[` +
	`{"id":"1002:744C-1EAE:7905-0000:03:00.0","name":"RX 7900 XTX"},` +
	`{"id":"1002:164E-0000:0000-0000:0c:00.0","name":"Raphael"}]}`

const lactStats = `{"status":"ok","data":{"fan":{"pwm_current":128},` +
	`"vram":{"total":25753026560,"used":1073741824},"power":{"average":0,"current":55.5},` +
	`"temps":{"edge":{"current":41},"junction":{"current":45},"mem":{"current":50}},"busy_percent":7}}`

func TestProbe_LactRead(t *testing.T) {
	socket := fakeLactd(t, map[string]string{
		"list_devices:": lactDevices,
		"device_stats:1002:744C-1EAE:7905-0000:03:00.0": lactStats,
		// iGPUs without VRAM accounting are skipped
		"device_stats:1002:164E-0000:0000-0000:0c:00.0": `{"status":"ok","data":{"vram":{"total":0}}}`,
	})

	samples, err := newLact(Options{Socket: socket}).Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 1)
	s := samples[0]
	assert.Equal(t, "0000:03:00.0", s.Key)
	assert.Equal(t, "RX 7900 XTX", s.Name)
	assert.Equal(t, 41.0, *s.TempC)
	assert.Equal(t, 50.0, *s.VramTempC)
	assert.InDelta(t, 50.2, *s.FanPct, 0.1)
	assert.Equal(t, 55.5, *s.PowerW, "current power when the average is 0")
	assert.Equal(t, 7.0, *s.UtilPct)
	assert.Equal(t, 24560, *s.VramTotalMB)
	assert.Equal(t, 1024, *s.VramUsedMB)
}

func TestProbe_LactSkipsSuspendedDevices(t *testing.T) {
	root := fakeSysfs(t)
	writeFiles(t, root, map[string]string{"devices/0000:03:00.0/power/runtime_status": "suspended"})
	symlink(t, root, "bus/pci/devices/0000:0c:00.0", "../../../devices/0000:03:00.0")
	socket := fakeLactd(t, map[string]string{"list_devices:": lactDevices})

	samples, err := newLact(Options{Socket: socket, SkipSuspended: true}).Read(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 1)
	assert.Equal(t, "0000:0c:00.0", samples[0].Key)
	assert.Equal(t, GpuStateSuspended, samples[0].State)
}

func TestProbe_LactMissingSocketIsUnavailable(t *testing.T) {
	_, err := newLact(Options{Socket: filepath.Join(t.TempDir(), "missing.sock")}).Read(context.Background())
	assert.True(t, errors.Is(err, ErrUnavailable))
}
