package swaputil

import "time"

const ProcessStateChangeEventID = 0x01
const ConfigFileChangedEventID = 0x03
const ActivityLogEventID = 0x05
const ModelPreloadedEventID = 0x06
const InFlightRequestsEventID = 0x07
const ProfileChangedEventID = 0x08
const ModelCapabilitiesChangedEventID = 0x09

// ProcessStateChangeEvent is emitted whenever a process transitions between
// lifecycle states. States are carried as strings so this package stays a leaf
// (no import of internal/process).
type ProcessStateChangeEvent struct {
	ProcessName string
	OldState    string
	NewState    string
	// Timestamp is when the transition actually happened, captured by the
	// emitter. Delivery through the dispatcher is asynchronous, so
	// subscribers must never substitute their own clock: a queued event
	// could be handled long after the transition.
	Timestamp time.Time
	// Elapsed is how long the process sat in OldState before this
	// transition, measured by the emitter (zero for emitters that do not
	// track it). A NewState of ready with OldState of starting therefore
	// carries the true load time regardless of delivery delays.
	Elapsed time.Duration
}

func (e ProcessStateChangeEvent) Type() uint32 {
	return ProcessStateChangeEventID
}

type ReloadingState int

const (
	ReloadingStateStart ReloadingState = iota
	ReloadingStateEnd
)

type ConfigFileChangedEvent struct {
	State ReloadingState
}

func (e ConfigFileChangedEvent) Type() uint32 {
	return ConfigFileChangedEventID
}

type ModelPreloadedEvent struct {
	ModelName string
	Success   bool
}

func (e ModelPreloadedEvent) Type() uint32 {
	return ModelPreloadedEventID
}

type InFlightRequestsEvent struct {
	Operation string                 `json:"operation"`
	Requests  []InflightRequestEntry `json:"requests,omitempty"`
	Request   *InflightRequestEntry  `json:"request,omitempty"`
	ID        string                 `json:"id,omitempty"`
}

func (e InFlightRequestsEvent) Type() uint32 {
	return InFlightRequestsEventID
}

type InflightRequestEntry struct {
	ID          string            `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	Model       string            `json:"model"`
	ReqPath     string            `json:"req_path"`
	Method      string            `json:"method"`
	ReqHeaders  map[string]string `json:"req_headers"`
	RemoteIP    string            `json:"remote_ip"`
	RespHeaders map[string]string `json:"resp_headers"`
	RespBytes   int64             `json:"resp_bytes"`
	ElapsedMs   int64             `json:"elapsed_ms"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type ProfileChangedEvent struct {
	Active string
}

func (e ProfileChangedEvent) Type() uint32 {
	return ProfileChangedEventID
}

// ModelCapabilitiesChangedEvent is emitted when capability discovery changes
// what a model advertises. Discovery has to wait for the upstream to answer,
// so it finishes well after the process reported itself ready, and the model
// listing pushed on that state change is already stale by then. Without this
// the UI shows the pre-discovery view until something else makes it re-read.
type ModelCapabilitiesChangedEvent struct {
	ModelID string
}

func (e ModelCapabilitiesChangedEvent) Type() uint32 {
	return ModelCapabilitiesChangedEventID
}
