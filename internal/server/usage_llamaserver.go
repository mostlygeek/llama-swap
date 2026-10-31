package server

import "time"

// llamaServerExtractor reads llama-server's timings block (prompt_n,
// predicted_n, rates, cache_n, draft_n*), which overrides the usage block
// counts. /infill responses are arrays with timings in the last element.
type llamaServerExtractor struct{}

func (llamaServerExtractor) newParser(_ string, start time.Time) usageParser {
	return newReaderParser(usageReaders{timings: true}, start)
}
