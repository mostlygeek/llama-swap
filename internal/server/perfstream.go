package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/perf/probe"
)

// perfKeepAlive keeps proxies from closing a quiet performance stream, for
// example while GPUs are paused because no model is loaded.
const perfKeepAlive = 30 * time.Second

// handleAPIPerformanceEvents streams performance readings as Server-Sent
// Events with one named event per kind and raw JSON data:
//
//	meta   {"every":{"system":5,"gpu":5},"gpus":[{"key":"0000:03:00.0","name":"RTX 4090"}]}
//	sys    {"t":[...],"cpu":[[core 0 series],...],"memUsed":[...],...}
//	gpu    {"t":[...],"gpus":[{"util":[...],"vramUsed":[...],...}]}
//	state  {"system":"ok","gpu":"paused","suspended":[0]}
//
// Readings are laid out in columns so each field name is sent once per
// message. On connect the stream sends meta, state and the last hour of
// readings; after that each sys or gpu message holds one reading. The gpus
// array follows the order in meta, and a section missing from meta.every is
// disabled. An open stream is a viewer, which lets poll: active read.
func (s *Server) handleAPIPerformanceEvents(w http.ResponseWriter, r *http.Request) {
	if s.perf == nil || !s.perf.Enabled() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]bool{"enabled": false})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")

	state, events, stop := s.perf.Watch()
	defer stop()

	enc := &perfEncoder{}
	write := func(name string, v any) bool {
		data, err := json.Marshal(v)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	write("meta", enc.meta(state.GPUs, state.SysEvery, state.GPUEvery))
	write("state", enc.status(state.Status))
	if len(state.Sys) > 0 {
		write("sys", sysColumns(state.Sys))
	}
	if len(state.GPU) > 0 {
		write("gpu", enc.gpuColumns(state.GPU))
	}

	keepAlive := time.NewTicker(perfKeepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdownCtx.Done():
			return
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case e := <-events:
			ok := true
			switch e.Kind {
			case perf.EventMeta:
				ok = write("meta", enc.meta(e.GPUs, state.SysEvery, state.GPUEvery))
			case perf.EventStatus:
				ok = write("state", enc.status(*e.Status))
			case perf.EventSys:
				ok = write("sys", sysColumns([]perf.SysStat{*e.Sys}))
			case perf.EventGPU:
				ok = write("gpu", enc.gpuColumns([]perf.GpuSnapshot{*e.GPU}))
			}
			if !ok {
				return
			}
		}
	}
}

// perfEncoder maps GPU keys to the index order sent in the last meta event.
type perfEncoder struct {
	index map[string]int
	count int
}

type perfMeta struct {
	Every map[string]float64 `json:"every"`
	GPUs  []perf.GpuInfo     `json:"gpus"`
}

func (e *perfEncoder) meta(gpus []perf.GpuInfo, sysEvery, gpuEvery time.Duration) perfMeta {
	e.index = make(map[string]int, len(gpus))
	for i, g := range gpus {
		e.index[g.Key] = i
	}
	e.count = len(gpus)
	m := perfMeta{Every: map[string]float64{}, GPUs: gpus}
	if m.GPUs == nil {
		m.GPUs = []perf.GpuInfo{}
	}
	if sysEvery > 0 {
		m.Every["system"] = sysEvery.Seconds()
	}
	if gpuEvery > 0 {
		m.Every["gpu"] = gpuEvery.Seconds()
	}
	return m
}

type perfState struct {
	System    perf.SectionState `json:"system"`
	GPU       perf.SectionState `json:"gpu"`
	Suspended []int             `json:"suspended,omitempty"`
}

func (e *perfEncoder) status(st perf.Status) perfState {
	out := perfState{System: st.System, GPU: st.GPU}
	for _, key := range st.Suspended {
		if i, ok := e.index[key]; ok {
			out.Suspended = append(out.Suspended, i)
		}
	}
	return out
}

// round1 rounds to one decimal place to keep the stream small.
func round1(v float64) float64 { return math.Round(v*10) / 10 }

type sysColumnsMsg struct {
	T         []int64                       `json:"t"`
	CPU       [][]float64                   `json:"cpu"`
	MemUsed   []int                         `json:"memUsed"`
	MemTotal  []int                         `json:"memTotal"`
	SwapUsed  []int                         `json:"swapUsed"`
	SwapTotal []int                         `json:"swapTotal"`
	Load1     []float64                     `json:"load1"`
	Load5     []float64                     `json:"load5"`
	Load15    []float64                     `json:"load15"`
	Net       map[string]map[string][]int64 `json:"net"`
}

// sysColumns lays system readings out as one array per field. cpu holds one
// series per core and net one recv and one sent series per interface (0
// where an interface is missing from a reading).
func sysColumns(stats []perf.SysStat) sysColumnsMsg {
	n := len(stats)
	m := sysColumnsMsg{
		T:         make([]int64, n),
		MemUsed:   make([]int, n),
		MemTotal:  make([]int, n),
		SwapUsed:  make([]int, n),
		SwapTotal: make([]int, n),
		Load1:     make([]float64, n),
		Load5:     make([]float64, n),
		Load15:    make([]float64, n),
		Net:       map[string]map[string][]int64{},
	}
	cores := 0
	for _, s := range stats {
		cores = max(cores, len(s.CpuUtilPerCore))
	}
	m.CPU = make([][]float64, cores)
	for c := range m.CPU {
		m.CPU[c] = make([]float64, n)
	}
	for i, s := range stats {
		m.T[i] = s.Timestamp.Unix()
		for c, v := range s.CpuUtilPerCore {
			m.CPU[c][i] = round1(v)
		}
		m.MemUsed[i], m.MemTotal[i] = s.MemUsedMB, s.MemTotalMB
		m.SwapUsed[i], m.SwapTotal[i] = s.SwapUsedMB, s.SwapTotalMB
		m.Load1[i], m.Load5[i], m.Load15[i] = round1(s.LoadAvg1), round1(s.LoadAvg5), round1(s.LoadAvg15)
		for _, io := range s.NetIO {
			iface := m.Net[io.Name]
			if iface == nil {
				iface = map[string][]int64{"recv": make([]int64, n), "sent": make([]int64, n)}
				m.Net[io.Name] = iface
			}
			iface["recv"][i] = int64(io.BytesRecv)
			iface["sent"][i] = int64(io.BytesSent)
		}
	}
	return m
}

type gpuColumnsMsg struct {
	T    []int64                 `json:"t"`
	GPUs []map[string][]*float64 `json:"gpus"`
}

// gpuFields are the GpuSample fields sent, by their stream name.
var gpuFields = []struct {
	name  string
	value func(probe.GpuSample) *float64
}{
	{"util", func(g probe.GpuSample) *float64 { return g.UtilPct }},
	{"vramUsed", func(g probe.GpuSample) *float64 { return intToFloat(g.VramUsedMB) }},
	{"vramTotal", func(g probe.GpuSample) *float64 { return intToFloat(g.VramTotalMB) }},
	{"sharedUsed", func(g probe.GpuSample) *float64 { return intToFloat(g.SharedUsedMB) }},
	{"sharedTotal", func(g probe.GpuSample) *float64 { return intToFloat(g.SharedTotalMB) }},
	{"tempC", func(g probe.GpuSample) *float64 { return g.TempC }},
	{"vramTempC", func(g probe.GpuSample) *float64 { return g.VramTempC }},
	{"fanPct", func(g probe.GpuSample) *float64 { return g.FanPct }},
	{"powerW", func(g probe.GpuSample) *float64 { return g.PowerW }},
}

func intToFloat(v *int) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v)
	return &f
}

// gpuColumns lays GPU readings out per GPU (in meta order) and per field. A
// value that was not reported, or a GPU missing from a reading, is null, and
// a field that is null across the whole message is left out.
func (e *perfEncoder) gpuColumns(snaps []perf.GpuSnapshot) gpuColumnsMsg {
	n := len(snaps)
	m := gpuColumnsMsg{T: make([]int64, n), GPUs: make([]map[string][]*float64, e.count)}
	values := make([]map[string][]*float64, e.count)
	for g := range values {
		values[g] = map[string][]*float64{}
	}
	for i, snap := range snaps {
		m.T[i] = snap.Timestamp.Unix()
		for _, sample := range snap.GPUs {
			g, ok := e.index[sample.Key]
			if !ok {
				continue
			}
			for _, f := range gpuFields {
				v := f.value(sample)
				if v == nil {
					continue
				}
				series := values[g][f.name]
				if series == nil {
					series = make([]*float64, n)
					values[g][f.name] = series
				}
				r := round1(*v)
				series[i] = &r
			}
		}
	}
	for g := range values {
		m.GPUs[g] = values[g]
	}
	return m
}
