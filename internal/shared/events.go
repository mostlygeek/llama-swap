package shared

const ConfigFileChangedEventID = 0x03
const ModelPreloadedEventID = 0x06
const InFlightRequestsEventID = 0x07

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
