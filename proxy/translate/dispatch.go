package translate

import "strings"

// Choose decides the target (upstream) protocol given the inbound client
// protocol and the set of protocols the resolved model supports.
//
// Rules:
//  1. If inbound is in supported → pass-through (needXlate=false).
//  2. Else prefer OpenAI when supported.
//  3. Else fall back to the first entry of supported.
func Choose(inbound Protocol, supported []Protocol) (target Protocol, needXlate bool) {
	for _, p := range supported {
		if p == inbound {
			return inbound, false
		}
	}
	for _, p := range supported {
		if p == ProtocolOpenAI {
			return ProtocolOpenAI, true
		}
	}
	if len(supported) > 0 {
		return supported[0], true
	}
	// Degenerate: pretend pass-through; caller will 501 anyway.
	return inbound, false
}

// --- Stateless tool-call ID mapping ---
//
// OpenAI ↔ Anthropic: reversible prefixed wrappers.
//   OAI id "call_XYZ" → sent to Anth as "toolu_oa_XYZ" and back to OAI
//   "call_XYZ" on tool_result.
//   Anth id "toolu_XYZ" → sent to OAI as "call_anth_XYZ" and back to Anth
//   "toolu_XYZ" on tool_result.
//
// Ollama has no IDs; upstream emission uses Ollama's positional tool_result
// and no ID rewriting is needed on the wire.

const (
	prefixOAtoAnth = "toolu_oa_"
	prefixAnthToOA = "call_anth_"
)

// MapToolIDOutbound rewrites a tool_use ID from sourceProto form to targetProto
// form when forwarding an assistant's tool_use to the target side.
func MapToolIDOutbound(source, target Protocol, id string) string {
	if id == "" {
		return id
	}
	if source == target {
		return id
	}
	switch {
	case source == ProtocolOpenAI && target == ProtocolAnthropic:
		return prefixOAtoAnth + stripPrefix(id, "call_")
	case source == ProtocolAnthropic && target == ProtocolOpenAI:
		return prefixAnthToOA + stripPrefix(id, "toolu_")
	}
	return id
}

// MapToolIDInbound reverses the transformation applied on a tool_result as it
// flows back through the proxy to the original protocol.
func MapToolIDInbound(source, target Protocol, id string) string {
	if id == "" {
		return id
	}
	if source == target {
		return id
	}
	switch {
	case source == ProtocolOpenAI && target == ProtocolAnthropic:
		// client is OpenAI, upstream is Anthropic: upstream's tool_use IDs
		// were minted by us as toolu_oa_<X>; strip the prefix back to call_<X>.
		if rest, ok := trimPrefix(id, prefixOAtoAnth); ok {
			return "call_" + rest
		}
	case source == ProtocolAnthropic && target == ProtocolOpenAI:
		if rest, ok := trimPrefix(id, prefixAnthToOA); ok {
			return "toolu_" + rest
		}
	}
	return id
}

func stripPrefix(s, prefix string) string {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

func trimPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return s, false
}
