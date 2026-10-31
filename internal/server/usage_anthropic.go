package server

import "time"

// anthropicExtractor reads Anthropic Messages usage: input_tokens,
// output_tokens and cache_read_input_tokens, from message.usage in the
// message_start event and usage in message_delta. Thinking tokens are part
// of output_tokens and not reported separately.
type anthropicExtractor struct{}

func (anthropicExtractor) newParser(_ string, start time.Time) usageParser {
	return newReaderParser(usageReaders{}, start)
}
