package server

import "github.com/tidwall/gjson"

// readAnthropicUsage reads Anthropic Messages usage: input_tokens,
// output_tokens, cache_read_input_tokens and cache_creation_input_tokens.
// Anthropic's input_tokens leaves out both cache counts, so they are added
// back (see apiUsage.toUsage) to make the input the whole prompt. A stream reports them in two
// places: message.usage in message_start (input and cache counts) and usage
// in message_delta (the final output count). A non-streaming response has a
// single top-level usage. Thinking tokens are part of output_tokens and are
// not reported separately, so reasoning stays unknown.
func readAnthropicUsage(ev gjson.Result, u *apiUsage) {
	for _, path := range []string{"message.usage", "usage"} {
		b := ev.Get(path)
		if !b.IsObject() {
			continue
		}
		found := setCount(&u.input, b.Get("input_tokens"))
		found = setCount(&u.output, b.Get("output_tokens")) || found
		found = setReported(&u.cached, b.Get("cache_read_input_tokens")) || found
		found = setCount(&u.cacheWrite, b.Get("cache_creation_input_tokens")) || found
		u.inputExcludesCache = true
		if found {
			u.found = true
		}
	}
}
