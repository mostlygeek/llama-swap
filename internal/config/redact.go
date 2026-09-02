package config

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// RedactedPlaceholder replaces every value the redactor removes.
const RedactedPlaceholder = "[REDACTED]"

// secretKeyHints are substrings matched case-insensitively against a mapping
// key. A matching value -- and everything nested under it -- is replaced with
// RedactedPlaceholder. This covers the structured secrets: apiKeys,
// peers.<id>.apiKey, and any macro or metadata key named like a credential.
var secretKeyHints = []string{
	"apikey", "api_key", "token", "secret", "password", "passwd", "credential", "bearer",
}

// secretValuePatterns match well-known credential formats wherever they land --
// inside a cmd string, an env entry, a macro, or free-form metadata -- so a
// secret is caught even when the key holding it looks innocent. Each pattern is
// specific enough that a model path or ordinary flag will not trip it.
var secretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`hf_[A-Za-z0-9]{16,}`),          // Hugging Face
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),        // OpenAI and lookalikes
	regexp.MustCompile(`gh[oprsu]_[A-Za-z0-9]{20,}`),   // GitHub tokens
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`), // Slack
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),             // AWS access key id
}

// cmdSecretPatterns each capture a leading group to keep and drop the value
// that follows: a credential-named flag, and an inline Authorization header as
// people often pass one via --header.
var cmdSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(--?[a-z0-9][a-z0-9-]*(?:key|token|secret|password|auth)[a-z0-9-]*[=\s]+)(\S+)`),
	regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*(?:bearer|basic)\s+)(\S+)`),
}

// envSecretName matches the NAME half of a NAME=VALUE env entry whose value
// should be blanked.
var envSecretName = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|credential)`)

// RedactedYAML marshals the effective configuration to YAML with credentials
// removed. Values shown are the resolved ones: defaults filled in, ${env.*}
// and macro references already expanded.
//
// When path is non-empty it selects a subtree by dotted key, for example
// "models" or "models.qwen3"; found is false when the path does not resolve.
//
// Redaction is layered: a value under a credential-named key is dropped whole,
// cmd/cmdStop strings and env entries have their secret arguments blanked, and
// any recognizable credential token is scrubbed wherever it appears.
func (c Config) RedactedYAML(path string) (out string, found bool, err error) {
	marshaled, err := yaml.Marshal(c) // #nosec G117 -- marshals config only as the first step of RedactedYAML, which then scrubs credential keys
	if err != nil {
		return "", false, fmt.Errorf("marshaling config: %w", err)
	}

	var tree any
	if err := yaml.Unmarshal(marshaled, &tree); err != nil {
		return "", false, fmt.Errorf("re-parsing config: %w", err)
	}

	node := tree
	if path != "" {
		node, found = navigateConfig(tree, strings.Split(path, "."))
		if !found {
			return "", false, nil
		}
	}

	// Drop zero-value defaults the struct carries so the output is the settings
	// that are actually set. Without this even a two-model config expands to
	// hundreds of lines of empty timeouts/filters/capabilities blocks and blows
	// past the tool result cap.
	rendered, _ := pruneEmpty(redactValue("", node))

	buf, err := yaml.Marshal(rendered)
	if err != nil {
		return "", false, fmt.Errorf("marshaling redacted config: %w", err)
	}
	return string(buf), true, nil
}

// pruneEmpty removes nil, empty strings, empty maps and empty slices. Booleans
// and numbers are kept: they are compact and a false or 0 can be deliberate.
func pruneEmpty(v any) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if pv, empty := pruneEmpty(val); !empty {
				out[k] = pv
			}
		}
		return out, len(out) == 0
	case []any:
		out := make([]any, 0, len(t))
		for _, val := range t {
			if pv, empty := pruneEmpty(val); !empty {
				out = append(out, pv)
			}
		}
		return out, len(out) == 0
	case string:
		return t, t == ""
	case nil:
		return nil, true
	default:
		return v, false
	}
}

// ConfigTopLevelKeys lists the keys available at the root of RedactedYAML, so a
// caller can suggest valid paths when one does not resolve.
func (c Config) ConfigTopLevelKeys() []string {
	marshaled, err := yaml.Marshal(c) // #nosec G117 -- marshals config only as the first step of RedactedYAML, which then scrubs credential keys
	if err != nil {
		return nil
	}
	var tree map[string]any
	if err := yaml.Unmarshal(marshaled, &tree); err != nil {
		return nil
	}
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	return keys
}

func navigateConfig(node any, keys []string) (any, bool) {
	cur := node
	for _, key := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func redactValue(key string, v any) any {
	if isSecretKey(key) {
		return redactWhole(v)
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = redactValue(k, val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			// Sequence elements keep the parent key: env stays "env", and a
			// bare list like apiKeys is already handled by isSecretKey above.
			out[i] = redactValue(key, val)
		}
		return out
	case string:
		return redactString(key, t)
	default:
		return v
	}
}

func redactWhole(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k := range t {
			out[k] = RedactedPlaceholder
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = RedactedPlaceholder
		}
		return out
	case nil:
		return nil
	default:
		return RedactedPlaceholder
	}
}

func redactString(key, s string) string {
	switch strings.ToLower(key) {
	case "cmd", "cmdstop":
		for _, re := range cmdSecretPatterns {
			s = re.ReplaceAllString(s, "${1}"+RedactedPlaceholder)
		}
	case "env":
		if i := strings.IndexByte(s, '='); i > 0 && envSecretName.MatchString(s[:i]) {
			return s[:i+1] + RedactedPlaceholder
		}
	}
	return scrubTokens(s)
}

func scrubTokens(s string) string {
	for _, re := range secretValuePatterns {
		s = re.ReplaceAllString(s, RedactedPlaceholder)
	}
	return s
}

func isSecretKey(key string) bool {
	if key == "" {
		return false
	}
	lower := strings.ToLower(key)
	for _, hint := range secretKeyHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}
