package translate

import (
	"fmt"
)

// Registry holds the protocol adapter implementations. Tests and handlers
// register adapters at init time (see pkg openai/anthropic/ollama).
type Registry struct {
	parsers       map[Protocol]Parser
	emitters      map[Protocol]Emitter
	streamParsers map[Protocol]func() StreamParser
	streamEmits   map[Protocol]func() StreamEmitter
}

var defaultRegistry = &Registry{
	parsers:       map[Protocol]Parser{},
	emitters:      map[Protocol]Emitter{},
	streamParsers: map[Protocol]func() StreamParser{},
	streamEmits:   map[Protocol]func() StreamEmitter{},
}

// Register registers adapter components for a protocol. Safe to call from
// adapter package init() functions.
func Register(p Protocol, parser Parser, emitter Emitter) {
	defaultRegistry.parsers[p] = parser
	defaultRegistry.emitters[p] = emitter
}

// RegisterStream registers streaming factories for a protocol.
func RegisterStream(p Protocol, parser func() StreamParser, emitter func() StreamEmitter) {
	defaultRegistry.streamParsers[p] = parser
	defaultRegistry.streamEmits[p] = emitter
}

// GetParser returns the registered parser or an error.
func GetParser(p Protocol) (Parser, error) {
	if a, ok := defaultRegistry.parsers[p]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("translate: no parser registered for %q", p)
}

// GetEmitter returns the registered emitter or an error.
func GetEmitter(p Protocol) (Emitter, error) {
	if a, ok := defaultRegistry.emitters[p]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("translate: no emitter registered for %q", p)
}

// NewStreamParser constructs a new StreamParser for p or returns an error.
func NewStreamParser(p Protocol) (StreamParser, error) {
	if f, ok := defaultRegistry.streamParsers[p]; ok {
		return f(), nil
	}
	return nil, fmt.Errorf("translate: no stream parser registered for %q", p)
}

// NewStreamEmitter constructs a new StreamEmitter for p or returns an error.
func NewStreamEmitter(p Protocol) (StreamEmitter, error) {
	if f, ok := defaultRegistry.streamEmits[p]; ok {
		return f(), nil
	}
	return nil, fmt.Errorf("translate: no stream emitter registered for %q", p)
}

// TranslateRequest parses a request body in the inbound protocol, applies
// any direction-specific rewrites (tool IDs), then emits the target
// protocol's wire-format body.
func TranslateRequest(inbound, target Protocol, kind EndpointKind, body []byte) ([]byte, error) {
	in, err := GetParser(inbound)
	if err != nil {
		return nil, err
	}
	out, err := GetEmitter(target)
	if err != nil {
		return nil, err
	}
	ir, err := in.ParseChatRequest(body, kind)
	if err != nil {
		return nil, err
	}
	rewriteRequestIDs(ir, inbound, target)
	return out.EmitChatRequest(ir, kind)
}

// TranslateResponse parses an upstream non-streaming response in target
// protocol and emits it in the inbound (client) protocol.
func TranslateResponse(inbound, target Protocol, body []byte) ([]byte, error) {
	in, err := GetParser(target)
	if err != nil {
		return nil, err
	}
	out, err := GetEmitter(inbound)
	if err != nil {
		return nil, err
	}
	ir, err := in.ParseChatResponse(body)
	if err != nil {
		return nil, err
	}
	rewriteResponseIDs(ir, inbound, target)
	return out.EmitChatResponse(ir)
}

// rewriteRequestIDs rewrites assistant tool_call IDs + tool_result IDs so
// that the target protocol sees an ID in its own namespace while the return
// path can reverse the transform.
func rewriteRequestIDs(ir *ChatRequest, inbound, target Protocol) {
	if inbound == target {
		return
	}
	for mi := range ir.Messages {
		m := &ir.Messages[mi]
		for ci := range m.Content {
			p := &m.Content[ci]
			switch p.Type {
			case PartToolUse:
				p.ToolUseID = MapToolIDOutbound(inbound, target, p.ToolUseID)
			case PartToolResult:
				// tool_result inbound IDs were minted in inbound's namespace;
				// forward to target's namespace so the target recognises them.
				p.ToolUseID = MapToolIDOutbound(inbound, target, p.ToolUseID)
			}
		}
		for ti := range m.ToolCalls {
			m.ToolCalls[ti].ID = MapToolIDOutbound(inbound, target, m.ToolCalls[ti].ID)
		}
		if m.ToolCallID != "" {
			m.ToolCallID = MapToolIDOutbound(inbound, target, m.ToolCallID)
		}
	}
}

// rewriteResponseIDs rewrites assistant tool_use IDs in the upstream's
// namespace back into the inbound client's namespace.
func rewriteResponseIDs(ir *ChatResponse, inbound, target Protocol) {
	if inbound == target {
		return
	}
	for ci := range ir.Message.Content {
		p := &ir.Message.Content[ci]
		if p.Type == PartToolUse {
			p.ToolUseID = MapToolIDInbound(inbound, target, p.ToolUseID)
		}
	}
	for ti := range ir.Message.ToolCalls {
		ir.Message.ToolCalls[ti].ID = MapToolIDInbound(inbound, target, ir.Message.ToolCalls[ti].ID)
	}
}
