package translate

import (
	"fmt"
	"io"
	"strings"
)

// Protocol names a wire format.
type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"
	ProtocolAnthropic Protocol = "anthropic"
	ProtocolOllama    Protocol = "ollama"
)

// ValidProtocols is the closed set accepted in configuration.
var ValidProtocols = []Protocol{ProtocolOpenAI, ProtocolAnthropic, ProtocolOllama}

// ParseProtocol parses a string into a Protocol or returns an error.
func ParseProtocol(s string) (Protocol, error) {
	switch Protocol(strings.ToLower(strings.TrimSpace(s))) {
	case ProtocolOpenAI:
		return ProtocolOpenAI, nil
	case ProtocolAnthropic:
		return ProtocolAnthropic, nil
	case ProtocolOllama:
		return ProtocolOllama, nil
	default:
		return "", fmt.Errorf("unknown protocol %q (valid: openai, anthropic, ollama)", s)
	}
}

// EndpointKind classifies an inbound chat-family endpoint. Translation runs
// only for these; every other route is pass-through.
type EndpointKind string

const (
	// KindChat is the rich chat endpoint for a protocol:
	//   openai    → POST /v1/chat/completions
	//   anthropic → POST /v1/messages
	//   ollama    → POST /api/chat
	KindChat EndpointKind = "chat"
	// KindGenerate is the single-prompt Ollama legacy endpoint:
	//   ollama    → POST /api/generate
	// (OpenAI /v1/completions is intentionally NOT in scope for v1.)
	KindGenerate EndpointKind = "generate"
)

// DetectInbound classifies the inbound request path into (protocol, kind)
// and whether this request is a chat-family request eligible for
// translation. Paths outside the chat family return (_, _, false) so the
// caller can skip the entire translation block.
func DetectInbound(method, path string) (Protocol, EndpointKind, bool) {
	if method != "POST" {
		return "", "", false
	}
	switch path {
	case "/v1/chat/completions":
		return ProtocolOpenAI, KindChat, true
	case "/v1/messages":
		return ProtocolAnthropic, KindChat, true
	case "/api/chat":
		return ProtocolOllama, KindChat, true
	case "/api/generate":
		return ProtocolOllama, KindGenerate, true
	}
	return "", "", false
}

// CanonicalPath returns the upstream path to use when forwarding a
// translated request to a model that natively speaks target.
func CanonicalPath(target Protocol, kind EndpointKind) string {
	switch target {
	case ProtocolOpenAI:
		return "/v1/chat/completions"
	case ProtocolAnthropic:
		return "/v1/messages"
	case ProtocolOllama:
		if kind == KindGenerate {
			return "/api/generate"
		}
		return "/api/chat"
	}
	return ""
}

// Parser turns a protocol's wire bytes into IR.
type Parser interface {
	// ParseChatRequest parses a full request body. For KindGenerate
	// inputs, the parser is expected to fold the legacy prompt into a
	// single user message.
	ParseChatRequest(body []byte, kind EndpointKind) (*ChatRequest, error)
	// ParseChatResponse parses a full non-streaming response body.
	ParseChatResponse(body []byte) (*ChatResponse, error)
}

// StreamParser turns upstream stream bytes into StreamEvents.
type StreamParser interface {
	// Feed is called with newly-arrived upstream bytes. It returns zero
	// or more StreamEvents fully decoded from complete frames. Partial
	// frames are buffered internally until the next Feed.
	Feed(chunk []byte) ([]StreamEvent, error)
	// Close is called when the upstream stream ends. It returns any
	// StreamEvents synthesized from buffered-but-unterminated state
	// (e.g. a final error event if upstream aborted mid-frame).
	Close() ([]StreamEvent, error)
}

// Emitter turns IR into a protocol's wire bytes.
type Emitter interface {
	EmitChatRequest(req *ChatRequest, kind EndpointKind) ([]byte, error)
	EmitChatResponse(resp *ChatResponse) ([]byte, error)
}

// StreamEmitter writes translated stream frames in the client's protocol.
type StreamEmitter interface {
	// ContentType returns the media type for the response Content-Type
	// header the client expects.
	ContentType() string
	// Emit writes one StreamEvent in the target wire format and flushes.
	Emit(w io.Writer, ev StreamEvent) error
}
