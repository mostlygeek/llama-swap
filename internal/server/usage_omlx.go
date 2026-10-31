package server

import "time"

// omlxExtractor reads oMLX usage, which embeds prompt_tokens_per_second and
// generation_tokens_per_second in the usage block.
type omlxExtractor struct{}

func (omlxExtractor) newParser(_ string, start time.Time) usageParser {
	return newReaderParser(usageReaders{}, start)
}
