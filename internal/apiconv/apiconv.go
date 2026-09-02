// Package apiconv translates LLM API requests and responses into and out of the
// OpenAI chat-completions wire format, which llama-swap uses as the canonical
// shape spoken by backends. An Anthropic /v1/messages request is converted to an
// OpenAI /v1/chat/completions request before dispatch, and the upstream OpenAI
// response is converted back to Anthropic shape (streaming and buffered).
//
// Backends are assumed to speak OpenAI; a model that natively speaks Anthropic
// opts out of translation via the passthroughAnthropic config flag, in which
// case the proxy forwards the request unchanged and never calls this package.
//
// The exported surface lives in the sibling files:
//   - AnthropicToOpenAIRequest  (anthropic_req.go)
//   - OpenAIToAnthropicResponse (anthropic_resp.go)
//   - NewAnthropicStreamWriter  (anthropic_stream.go)
//   - NewBufferingWriter        (buffer.go)
package apiconv
