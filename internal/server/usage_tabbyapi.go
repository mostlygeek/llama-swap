package server

import "time"

// tabbyAPIExtractor reads TabbyAPI usage, which embeds
// prompt_tokens_per_sec, completion_tokens_per_sec and total_time in the
// usage block.
type tabbyAPIExtractor struct{}

func (tabbyAPIExtractor) newParser(_ string, start time.Time) usageParser {
	return newReaderParser(usageReaders{}, start)
}
