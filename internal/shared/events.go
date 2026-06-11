package shared

const ProcessStateChangeEventID = 0x01
const ConfigFileChangedEventID = 0x03
const ActivityLogEventID = 0x05
const ModelPreloadedEventID = 0x06
const InFlightRequestsEventID = 0x07
const BackendBuildProgressEventID = 0x08
const ModelDownloadProgressEventID = 0x09

// ProcessStateChangeEvent is emitted whenever a process transitions between
// lifecycle states. States are carried as strings so this package stays a leaf
// (no import of internal/process).
type ProcessStateChangeEvent struct {
	ProcessName string
	OldState    string
	NewState    string
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
	Total int
}

func (e InFlightRequestsEvent) Type() uint32 {
	return InFlightRequestsEventID
}

// ProgressState indicates the current status of a long-running task.
type ProgressState string

const (
	ProgressRunning   ProgressState = "running"
	ProgressCompleted ProgressState = "completed"
	ProgressFailed    ProgressState = "failed"
	ProgressCancelled ProgressState = "cancelled"
)

// BackendBuildProgressEvent is emitted during a llama.cpp backend build.
type BackendBuildProgressEvent struct {
	TaskID  string       `json:"taskID"`
	Repo    string       `json:"repo"`
	Branch  string       `json:"branch"`
	State   ProgressState `json:"state"`
	Message string       `json:"message"`
	Pct     int          `json:"pct"`
}

func (e BackendBuildProgressEvent) Type() uint32 {
	return BackendBuildProgressEventID
}

// ModelDownloadProgressEvent is emitted during a HuggingFace model download.
type ModelDownloadProgressEvent struct {
	TaskID   string       `json:"taskID"`
	ModelID  string       `json:"modelID"`
	Filename string       `json:"filename"`
	State    ProgressState `json:"state"`
	Message  string       `json:"message"`
	Pct      int          `json:"pct"`
}

func (e ModelDownloadProgressEvent) Type() uint32 {
	return ModelDownloadProgressEventID
}
