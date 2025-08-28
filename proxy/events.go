package proxy

// package level registry of the different event types

const ProcessStateChangeEventID = 0x01
const ChatCompletionStatsEventID = 0x02
const ConfigFileChangedEventID = 0x03
const LogDataEventID = 0x04
const TokenMetricsEventID = 0x05
const ModelPreloadedEventID = 0x06

type ProcessStateChangeEvent struct {
	ProcessName string
	NewState    ProcessState
	OldState    ProcessState
}

func (e ProcessStateChangeEvent) Type() uint32 {
	return ProcessStateChangeEventID
}

type ChatCompletionStats struct {
	TokensGenerated int
}

func (e ChatCompletionStats) Type() uint32 {
	return ChatCompletionStatsEventID
}

type ReloadingState int

const (
	ReloadingStateStart ReloadingState = iota
	ReloadingStateEnd
)

type ConfigFileChangedEvent struct {
	ReloadingState ReloadingState
}

func (e ConfigFileChangedEvent) Type() uint32 {
	return ConfigFileChangedEventID
}

type LogDataEvent struct {
	Data []byte
}

func (e LogDataEvent) Type() uint32 {
	return LogDataEventID
}

type ModelPreloadedEvent struct {
	ModelName string
	Success   bool
}

func (e ModelPreloadedEvent) Type() uint32 {
	return ModelPreloadedEventID
}
