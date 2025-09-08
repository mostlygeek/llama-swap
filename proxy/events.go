package proxy

import "github.com/mostlygeek/llama-swap/event"

// package level registry of the different event types
const (
	ProcessStateChangeEventID event.EventType = iota
	ChatCompletionStatsEventID
	ConfigFileChangedEventID
	LogDataEventID
	TokenMetricsEventID
	ModelPreloadedEventID
)

type ProcessStateChangeEvent struct {
	ProcessName string
	NewState    ProcessState
	OldState    ProcessState
}

func (e ProcessStateChangeEvent) Type() event.EventType {
	return ProcessStateChangeEventID
}

type ChatCompletionStats struct {
	TokensGenerated int
}

func (e ChatCompletionStats) Type() event.EventType {
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

func (e ConfigFileChangedEvent) Type() event.EventType {
	return ConfigFileChangedEventID
}

type LogDataEvent struct {
	Data []byte
}

func (e LogDataEvent) Type() event.EventType {
	return LogDataEventID
}

type ModelPreloadedEvent struct {
	ModelName string
	Success   bool
}

func (e ModelPreloadedEvent) Type() event.EventType {
	return ModelPreloadedEventID
}
