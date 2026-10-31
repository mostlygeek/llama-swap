package server

import "time"

// openaiExtractor reads the OpenAI usage block: prompt/completion tokens,
// prompt_tokens_details.cached_tokens and
// completion_tokens_details.reasoning_tokens. It serves OpenAI, OpenRouter,
// halogen and any other OpenAI-compatible server.
type openaiExtractor struct{}

func (openaiExtractor) newParser(_ string, start time.Time) usageParser {
	return newReaderParser(usageReaders{}, start)
}
