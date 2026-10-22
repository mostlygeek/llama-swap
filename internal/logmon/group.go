package logmon

import "io"

// StreamID names one of a Group's log streams.
type StreamID string

// Stream IDs accepted by Group.Stream.
const (
	StreamProxy    StreamID = "proxy"
	StreamUpstream StreamID = "upstream"
	StreamHTTP     StreamID = "http"
)

// Group holds llama-swap's log streams. Each stream keeps its own history and
// subscribers. Streams selected for stdout also write into MuxLogs, which
// holds the combined history served by GET /logs and /logs/stream.
type Group struct {
	MuxLogs      *Monitor
	ProxyLogs    *Monitor
	UpstreamLogs *Monitor
	HttpLogs     *Monitor
}

// NewGroup builds the log streams. The proxy, upstream and http flags choose
// which streams are copied to stdout through MuxLogs; the others are only
// kept in their own history. MuxLogs discards its output when no stream is
// selected.
func NewGroup(stdout io.Writer, proxy, upstream, http bool) *Group {
	muxOut := io.Discard
	if proxy || upstream || http {
		muxOut = stdout
	}
	mux := NewWriter(muxOut)

	out := func(enabled bool) io.Writer {
		if enabled {
			return mux
		}
		return io.Discard
	}

	return &Group{
		MuxLogs:      mux,
		ProxyLogs:    NewWriter(out(proxy)),
		UpstreamLogs: NewWriter(out(upstream)),
		HttpLogs:     NewWriter(out(http)),
	}
}

// Stream returns the monitor for a stream ID.
func (g *Group) Stream(id StreamID) (*Monitor, bool) {
	switch id {
	case StreamProxy:
		return g.ProxyLogs, true
	case StreamUpstream:
		return g.UpstreamLogs, true
	case StreamHTTP:
		return g.HttpLogs, true
	default:
		return nil, false
	}
}

// SetLogLevel sets the log level of the proxy, upstream and http streams.
func (g *Group) SetLogLevel(level Level) {
	for _, m := range g.streams() {
		m.SetLogLevel(level)
	}
}

// SetLogTimeFormat sets the timestamp format of the proxy, upstream and http
// streams.
func (g *Group) SetLogTimeFormat(timeFormat string) {
	for _, m := range g.streams() {
		m.SetLogTimeFormat(timeFormat)
	}
}

func (g *Group) streams() []*Monitor {
	return []*Monitor{g.ProxyLogs, g.UpstreamLogs, g.HttpLogs}
}
