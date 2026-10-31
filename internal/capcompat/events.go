package capcompat

import "github.com/mostlygeek/llama-swap/internal/swaputil"

// DetectedEvent is published by a local process after it has run Detect
// against its own upstream, once per start. Err is set when detection
// failed; ErrUnsupportedUpstream is a normal outcome for servers with no
// capability surface. Service.Record stores the result.
type DetectedEvent struct {
	ModelID string
	// Key is LocalKey for the model, so the receiver does not need the
	// model config to store the result.
	Key  string
	Info Info
	Err  error
}

func (DetectedEvent) Type() uint32 { return swaputil.UpstreamDetectedEventID }
