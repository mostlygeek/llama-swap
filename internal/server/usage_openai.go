package server

import "github.com/tidwall/gjson"

// readOpenAIUsage reads the OpenAI usage block:
//
//   - chat/completions, completions, embeddings: usage.prompt_tokens,
//     completion_tokens, prompt_tokens_details.cached_tokens and
//     completion_tokens_details.reasoning_tokens
//   - Responses API: usage (non-streaming) or response.usage (the
//     response.completed event) with input_tokens, output_tokens,
//     input_tokens_details.cached_tokens and
//     output_tokens_details.reasoning_tokens
func readOpenAIUsage(ev gjson.Result, u *apiUsage) {
	for _, path := range []string{"usage", "response.usage"} {
		b := ev.Get(path)
		if !b.IsObject() {
			continue
		}
		found := false
		if v := b.Get("prompt_tokens"); v.Exists() {
			found = setCount(&u.input, v) || found
		} else {
			found = setCount(&u.input, b.Get("input_tokens")) || found
		}
		if v := b.Get("completion_tokens"); v.Exists() {
			found = setCount(&u.output, v) || found
		} else {
			found = setCount(&u.output, b.Get("output_tokens")) || found
		}
		if !setReported(&u.cached, b.Get("prompt_tokens_details.cached_tokens")) {
			setReported(&u.cached, b.Get("input_tokens_details.cached_tokens"))
		}
		if !setReported(&u.reasoning, b.Get("completion_tokens_details.reasoning_tokens")) {
			setReported(&u.reasoning, b.Get("output_tokens_details.reasoning_tokens"))
		}
		if found {
			u.found = true
		}
	}
}
