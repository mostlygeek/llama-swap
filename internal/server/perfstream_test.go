package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/perf/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fptr(v float64) *float64 { return &v }
func iptr(v int) *int         { return &v }

func TestServer_PerfSysColumns(t *testing.T) {
	t0 := time.Unix(1761300000, 0)
	msg := sysColumns([]perf.SysStat{
		{Timestamp: t0, CpuUtilPerCore: []float64{10.04, 20.06}, MemUsedMB: 100, MemTotalMB: 1000, LoadAvg1: 1.234,
			NetIO: []perf.NetIOStat{{Name: "eth0", BytesRecv: 5, BytesSent: 6}}},
		{Timestamp: t0.Add(5 * time.Second), CpuUtilPerCore: []float64{30, 40}, MemUsedMB: 200, MemTotalMB: 1000},
	})

	assert.Equal(t, []int64{1761300000, 1761300005}, msg.T)
	assert.Equal(t, [][]float64{{10, 30}, {20.1, 40}}, msg.CPU, "one series per core, rounded")
	assert.Equal(t, []int{100, 200}, msg.MemUsed)
	assert.Equal(t, []float64{1.2, 0}, msg.Load1)
	assert.Equal(t, []int64{5, 0}, msg.Net["eth0"]["recv"])
}

func TestServer_PerfGpuColumns(t *testing.T) {
	enc := &perfEncoder{}
	enc.meta([]perf.GpuInfo{{Key: "a", Name: "A"}, {Key: "b", Name: "B"}}, 0, 5*time.Second)
	t0 := time.Unix(1761300000, 0)

	msg := enc.gpuColumns([]perf.GpuSnapshot{
		{Timestamp: t0, GPUs: []probe.GpuSample{
			{Key: "a", State: probe.GpuStateOK, UtilPct: fptr(87.54), VramUsedMB: iptr(20480)},
			{Key: "b", State: probe.GpuStateSuspended},
		}},
		{Timestamp: t0.Add(5 * time.Second), GPUs: []probe.GpuSample{
			{Key: "a", State: probe.GpuStateOK, UtilPct: fptr(90)},
			{Key: "unknown", State: probe.GpuStateOK, UtilPct: fptr(1)},
		}},
	})

	data, err := json.Marshal(msg)
	require.NoError(t, err)
	assert.JSONEq(t, `{"t":[1761300000,1761300005],"gpus":[`+
		`{"util":[87.5,90],"vramUsed":[20480,null]},`+
		`{}]}`, string(data))
}

func TestServer_PerfMetaAndState(t *testing.T) {
	enc := &perfEncoder{}
	meta := enc.meta(nil, 5*time.Second, 0)
	data, _ := json.Marshal(meta)
	assert.JSONEq(t, `{"every":{"system":5},"gpus":[]}`, string(data))

	enc.meta([]perf.GpuInfo{{Key: "a"}, {Key: "b"}}, 5*time.Second, 5*time.Second)
	st := enc.status(perf.Status{System: perf.StateOK, GPU: perf.StateOK, Suspended: []string{"b", "gone"}})
	data, _ = json.Marshal(st)
	assert.JSONEq(t, `{"system":"ok","gpu":"ok","suspended":[1]}`, string(data))
}

func TestServer_PerfStreamDisabled(t *testing.T) {
	s := newTestServer(nil, nil)
	rec := httptest.NewRecorder()
	s.handleAPIPerformanceEvents(rec, httptest.NewRequest(http.MethodGet, "/api/events/performance", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.JSONEq(t, `{"enabled":false}`, rec.Body.String())

	rec = httptest.NewRecorder()
	s.handleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestServer_PerfStreamSendsInitialEvents(t *testing.T) {
	c := config.DefaultPerformanceConfig()
	c.GPU.Disabled = true
	mon, err := perf.New(c, logmon.NewWriter(io.Discard), nil)
	require.NoError(t, err)
	mon.Start()
	t.Cleanup(mon.Stop)

	s := newTestServer(nil, nil)
	s.perf = mon
	srv := httptest.NewServer(http.HandlerFunc(s.handleAPIPerformanceEvents))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// meta and state come first; system stats (poll: always) follow
	seen := map[string]string{}
	scanner := bufio.NewScanner(resp.Body)
	var name string
	for scanner.Scan() && len(seen) < 3 {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			seen[name] = strings.TrimPrefix(line, "data: ")
		}
	}
	require.Contains(t, seen, "meta")
	assert.JSONEq(t, `{"every":{"system":5},"gpus":[]}`, seen["meta"])
	require.Contains(t, seen, "state")
	assert.Contains(t, seen["state"], `"gpu":"disabled"`)
	require.Contains(t, seen, "sys")
	assert.Contains(t, seen["sys"], `"memTotal":[`)
}
