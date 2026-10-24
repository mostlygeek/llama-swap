package perf

import (
	"time"

	"github.com/mostlygeek/llama-swap/internal/perf/probe"
)

// SysStat is one reading of CPU, memory, load and network.
type SysStat struct {
	Timestamp time.Time `json:"timestamp"`

	CpuUtilPerCore []float64   `json:"cpu_util_per_core"`
	MemTotalMB     int         `json:"mem_total_mb"`
	MemUsedMB      int         `json:"mem_used_mb"`
	MemFreeMB      int         `json:"mem_free_mb"`
	SwapTotalMB    int         `json:"swap_total_mb"`
	SwapUsedMB     int         `json:"swap_used_mb"`
	LoadAvg1       float64     `json:"load_avg_1"`
	LoadAvg5       float64     `json:"load_avg_5"`
	LoadAvg15      float64     `json:"load_avg_15"`
	NetIO          []NetIOStat `json:"net_io"`
}

type NetIOStat struct {
	Name      string `json:"name"`
	BytesRecv uint64 `json:"bytes_recv"`
	BytesSent uint64 `json:"bytes_sent"`
}

// GpuSnapshot is one GPU reading: the merged samples of every probe taken
// at the same tick.
type GpuSnapshot struct {
	Timestamp time.Time
	GPUs      []probe.GpuSample
}

// GpuInfo identifies a GPU in the order it was first seen. That order is the
// GPU index used by the event stream.
type GpuInfo struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// SectionState says whether a section is taking readings.
type SectionState string

const (
	StateOK       SectionState = "ok"
	StatePaused   SectionState = "paused"
	StateDisabled SectionState = "disabled"
)

// Status is the monitor's current state, sent to watchers when it changes.
type Status struct {
	System SectionState
	GPU    SectionState
	// Suspended holds the keys of GPUs that were runtime suspended at the
	// last GPU reading.
	Suspended []string
}

// EventKind names what an Event carries.
type EventKind string

const (
	EventMeta   EventKind = "meta"
	EventSys    EventKind = "sys"
	EventGPU    EventKind = "gpu"
	EventStatus EventKind = "state"
)

// Event is sent to subscribers. Only the field matching Kind is set.
type Event struct {
	Kind   EventKind
	GPUs   []GpuInfo // EventMeta
	Sys    *SysStat
	GPU    *GpuSnapshot
	Status *Status
}
