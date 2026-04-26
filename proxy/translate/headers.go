package translate

import "net/http"

// TranslateHeaders adjusts request headers when an inbound request is being
// forwarded to an upstream that speaks a different protocol. Mostly this
// means bridging the two common authentication conventions (Anthropic's
// x-api-key vs OpenAI's Authorization: Bearer …) and defaulting any
// protocol-specific headers the upstream expects.
func TranslateHeaders(inbound, target Protocol, h http.Header) {
	if inbound == target {
		return
	}
	switch {
	case inbound == ProtocolOpenAI && target == ProtocolAnthropic:
		// OpenAI uses Authorization; Anthropic wants x-api-key.
		if h.Get("x-api-key") == "" {
			if auth := h.Get("Authorization"); len(auth) > 7 && auth[:7] == "Bearer " {
				h.Set("x-api-key", auth[7:])
			}
		}
		if h.Get("anthropic-version") == "" {
			h.Set("anthropic-version", "2023-06-01")
		}
	case inbound == ProtocolAnthropic && target == ProtocolOpenAI:
		if h.Get("Authorization") == "" {
			if k := h.Get("x-api-key"); k != "" {
				h.Set("Authorization", "Bearer "+k)
			}
		}
	}
	// Ollama does not authenticate by convention; no-op in its direction.
}
