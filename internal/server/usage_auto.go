package server

import "time"

// autoExtractor reads every block any supported upstream reports, later
// values winning. It is used when the upstream is unknown, and for llama-swap
// peers, which pass through whatever their own upstream returns.
type autoExtractor struct{}

func (autoExtractor) newParser(_ string, start time.Time) usageParser {
	return newReaderParser(usageReaders{timings: true, metrics: true}, start)
}
