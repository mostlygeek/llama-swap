package probe

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

const lactTimeout = 5 * time.Second

// lact reads GPUs through the LACT daemon's unix socket. See
// https://github.com/ilya-zlobintsev/LACT.
type lact struct {
	socket        string
	skipSuspended bool
}

func newLact(opts Options) *lact {
	return &lact{socket: opts.Socket, skipSuspended: opts.SkipSuspended}
}

func (p *lact) Name() string { return "lact" }

func (p *lact) Read(ctx context.Context) ([]GpuSample, error) {
	socket := p.socket
	if socket == "" {
		socket = lactSocketPath()
	}
	if socket == "" {
		return nil, fmt.Errorf("lact: no daemon socket: %w", ErrUnavailable)
	}

	var d net.Dialer
	dialCtx, cancel := context.WithTimeout(ctx, lactTimeout)
	defer cancel()
	conn, err := d.DialContext(dialCtx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("lact: %w: %w", ErrUnavailable, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(lactTimeout))
	reader := bufio.NewReader(conn)

	var devices []lactDeviceEntry
	if err := lactRequest(conn, reader, "list_devices", nil, &devices); err != nil {
		return nil, err
	}

	var samples []GpuSample
	for i, dev := range devices {
		key := gpuKey(lactPCIAddress(dev.ID), "", "lact", i)
		if p.skipSuspended && runtimeSuspended(filepath.Join(sysfsRoot, "bus", "pci", "devices", key)) {
			samples = append(samples, GpuSample{Key: key, Name: dev.Name, State: GpuStateSuspended})
			continue
		}
		var stats lactDeviceStats
		if err := lactRequest(conn, reader, "device_stats", map[string]string{"id": dev.ID}, &stats); err != nil {
			continue
		}
		// iGPUs without VRAM accounting report 0 and are not worth a panel.
		if stats.Vram.Total == nil || *stats.Vram.Total == 0 {
			continue
		}
		samples = append(samples, stats.sample(key, dev.Name))
	}
	return samples, nil
}

type lactDeviceEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type lactDeviceStats struct {
	Fan struct {
		PwmCurrent *uint8 `json:"pwm_current"`
	} `json:"fan"`
	Vram struct {
		Total *uint64 `json:"total"`
		Used  *uint64 `json:"used"`
	} `json:"vram"`
	Power struct {
		Average *float64 `json:"average"`
		Current *float64 `json:"current"`
	} `json:"power"`
	Temps map[string]struct {
		Current *float64 `json:"current"`
	} `json:"temps"`
	BusyPercent *uint8 `json:"busy_percent"`
}

func (st lactDeviceStats) sample(key, name string) GpuSample {
	s := GpuSample{Key: key, Name: name, State: GpuStateOK}
	if st.Vram.Total != nil {
		s.VramTotalMB = ptr(int(*st.Vram.Total / bytesPerMB))
	}
	if st.Vram.Used != nil {
		s.VramUsedMB = ptr(int(*st.Vram.Used / bytesPerMB))
	}
	if st.BusyPercent != nil {
		s.UtilPct = ptr(float64(*st.BusyPercent))
	}
	if st.Fan.PwmCurrent != nil {
		s.FanPct = ptr(float64(*st.Fan.PwmCurrent) / 255 * 100)
	}
	if st.Power.Average != nil && *st.Power.Average > 0 {
		s.PowerW = ptr(*st.Power.Average)
	} else if st.Power.Current != nil {
		s.PowerW = ptr(*st.Power.Current)
	}
	for _, k := range []string{"edge", "junction"} {
		if t, ok := st.Temps[k]; ok && t.Current != nil {
			s.TempC = ptr(*t.Current)
			break
		}
	}
	if s.TempC == nil {
		for _, t := range st.Temps {
			if t.Current != nil {
				s.TempC = ptr(*t.Current)
				break
			}
		}
	}
	// NVIDIA calls it "VRAM", AMD "mem".
	for _, k := range []string{"mem", "VRAM"} {
		if t, ok := st.Temps[k]; ok && t.Current != nil && *t.Current > 0 {
			s.VramTempC = ptr(*t.Current)
			break
		}
	}
	return s
}

// lactPCIAddress extracts the PCI address from a LACT device ID such as
// 1002:744C-1EAE:7905-0000:03:00.0.
func lactPCIAddress(id string) string {
	if i := strings.LastIndex(id, "-"); i >= 0 {
		return id[i+1:]
	}
	return id
}

func lactSocketPath() string {
	if p := os.Getenv("LACT_DAEMON_SOCKET_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if _, err := os.Stat("/run/lactd.sock"); err == nil {
		return "/run/lactd.sock"
	}
	if u, err := user.Current(); err == nil {
		p := filepath.Join("/run/user", u.Uid, "lactd.sock")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// lactRequest sends one newline-delimited JSON command and decodes the data
// of an "ok" response into out.
func lactRequest(conn net.Conn, reader *bufio.Reader, command string, args any, out any) error {
	req := struct {
		Command string `json:"command"`
		Args    any    `json:"args,omitempty"`
	}{command, args}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("lact %s: %w", command, err)
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("lact %s: %w", command, err)
	}
	var resp struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("lact %s: %w", command, err)
	}
	if resp.Status != "ok" {
		return fmt.Errorf("lact %s: %s", command, string(resp.Data))
	}
	return json.Unmarshal(resp.Data, out)
}
