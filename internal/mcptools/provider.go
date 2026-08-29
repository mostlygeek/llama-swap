// Package mcptools holds the tools llama-swap serves over MCP, independent of
// the HTTP and JSON-RPC transport in internal/server.
//
// The split mirrors internal/router vs internal/server: transport in one place,
// the thing being served in another. It is what lets a future provider that
// proxies an upstream MCP endpoint live outside the HTTP layer.
package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// NameSeparator joins a provider ID to a tool's local name. Two underscores
// are valid both in MCP tool names and in the OpenAI function-name convention
// that the Playground maps them into, unlike the "." MCP also permits.
const NameSeparator = "__"

// MaxNameLen bounds a fully qualified tool name.
//
// MCP itself allows 128 characters and the character class [a-zA-Z0-9_-.], but
// tool names reach models through an OpenAI-shaped `tools` array, whose
// convention is [a-zA-Z0-9_-] with a 64 character limit. Holding every name to
// the tighter of the two means a name that is valid here is usable everywhere.
const MaxNameLen = 64

// Tool is one advertised tool. Field tags are the MCP tools/list wire shape so
// the transport can marshal these directly.
type Tool struct {
	// Name is the tool's local name. The registry qualifies it with the
	// provider ID before it reaches a client.
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations *Annotations   `json:"annotations,omitempty"`

	// Tags are internal metadata for a future find_tools; never serialized.
	Tags []string `json:"-"`
}

// Annotations are hints clients use to decide how much ceremony a call needs.
//
// The two are independent and must be set per tool. A documentation lookup is
// read-only and idempotent; a clock reading is read-only and emphatically not
// idempotent, since the answer changing is the entire point.
type Annotations struct {
	ReadOnlyHint   bool `json:"readOnlyHint"`
	IdempotentHint bool `json:"idempotentHint"`
}

// Result is a tool's outcome.
//
// IsError means the tool ran but could not answer -- an unknown document id, a
// search with no matches, an upstream that timed out. That is reported to the
// model rather than to the transport, so it can read the explanation and retry.
type Result struct {
	Content   string
	IsError   bool
	Truncated bool
}

// Provider supplies one namespace of tools.
//
// Providers deal in local names throughout: Tools returns them unqualified and
// Call receives them unqualified. The registry owns namespacing entirely, so a
// provider never has to know its own prefix -- which matters most for a proxy
// provider, which forwards whatever names an upstream happens to advertise.
//
// Call returns a non-nil error only for protocol-level problems: an unknown
// tool, or a provider that is not usable at all. Anything a model could react
// to belongs in Result.IsError instead. Getting this backwards is the most
// common way a proxy turns a recoverable hiccup into a dead conversation.
type Provider interface {
	// ID is the namespace for this provider's tools.
	ID() string

	// Tools lists the provider's tools in a stable order.
	Tools(ctx context.Context) ([]Tool, error)

	// Call runs one tool by its local name.
	Call(ctx context.Context, name string, args map[string]json.RawMessage) (Result, error)

	// Shutdown releases any resources the provider holds, waiting up to
	// timeout for in-flight work. It matches router.Router's signature
	// because Server.Shutdown treats it the same way.
	Shutdown(timeout time.Duration) error
}

// QualifyName joins a provider ID and a local tool name.
func QualifyName(providerID, name string) string {
	return providerID + NameSeparator + name
}

// SplitName separates a qualified tool name into its provider ID and local
// name. Only the first separator is significant, so a local name may itself
// contain one.
func SplitName(qualified string) (providerID, name string, ok bool) {
	providerID, name, ok = strings.Cut(qualified, NameSeparator)
	if !ok || providerID == "" || name == "" {
		return "", "", false
	}
	return providerID, name, true
}

// ValidateName reports whether a fully qualified tool name is usable by every
// client llama-swap serves. See MaxNameLen for why the rules are stricter than
// MCP's own.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("tool name is empty")
	}
	if len(name) > MaxNameLen {
		return fmt.Errorf("tool name %q is %d characters, over the %d limit", name, len(name), MaxNameLen)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return fmt.Errorf("tool name %q contains %q, which is not allowed in an OpenAI function name", name, string(r))
		}
	}
	return nil
}
