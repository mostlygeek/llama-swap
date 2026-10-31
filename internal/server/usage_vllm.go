package server

import "time"

// vllmExtractor reads the usage block plus vLLM's metrics block:
// time_to_first_token_ms and mean_itl_ms for rates, and
// speculative_decoding counters when vLLM runs with
// --per-request-spec-decode-metrics.
type vllmExtractor struct{}

func (vllmExtractor) newParser(_ string, start time.Time) usageParser {
	return newReaderParser(usageReaders{metrics: true}, start)
}
