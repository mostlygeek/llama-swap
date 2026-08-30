package mcptools

import (
	"encoding/json"
	"strings"
)

// MaxToolResultBytes caps tool results intended for a model's context window.
// It is exported so providers in sibling packages and transport tests can use
// the same limit.
const MaxToolResultBytes = 16 * 1024

// CapResult truncates content to max bytes and marks the result when it does.
func CapResult(content string, max int) Result {
	capped, truncated := capText(content, max)
	if truncated {
		capped += "\n[truncated]"
	}
	return Result{Content: capped, Truncated: truncated}
}

// capText truncates s at the last newline before maxBytes. It reports whether
// anything was removed.
func capText(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s, false
	}
	cut := s[:maxBytes]
	if idx := strings.LastIndexByte(cut, '\n'); idx > 0 {
		cut = cut[:idx]
	}
	return cut, true
}

// StringArg reads a string tool argument, tolerating the malformed bare
// values that small models occasionally emit.
func StringArg(args map[string]json.RawMessage, key string) string {
	raw, ok := args[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.Trim(strings.TrimSpace(string(raw)), `"`)
	}
	return strings.TrimSpace(value)
}

// IntArg reads an integer tool argument, including a stringified integer.
func IntArg(args map[string]json.RawMessage, key string, fallback int) int {
	raw, ok := args[key]
	if !ok {
		return fallback
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		var asString string
		if json.Unmarshal(raw, &asString) == nil {
			if json.Unmarshal([]byte(asString), &value) == nil {
				return value
			}
		}
		return fallback
	}
	return value
}
