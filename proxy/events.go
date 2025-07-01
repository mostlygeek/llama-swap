package proxy

// package level registry of the different event types

const ProcessStateChangeEventID = 0x01

type ProcessStateChangeEvent struct {
	ProcessName string
	NewState    ProcessState
	OldState    ProcessState
}

func (e ProcessStateChangeEvent) Type() uint32 {
	return ProcessStateChangeEventID
}
