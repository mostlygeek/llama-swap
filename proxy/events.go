package proxy

// package level registry of the different event types

const ProcessStateChangeEventID = 0x01
const ChatCompletionStatsEventID = 0x02
const ConfigFileChangedEventID = 0x03

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

type ConfigFileChangedEvent struct {
	ReloadingState string
}

func (e ConfigFileChangedEvent) Type() uint32 {
	return ConfigFileChangedEventID
}
