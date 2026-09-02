package config

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// ProtectedParams is a list of parameters that cannot be set or stripped via filters
// These are protected to prevent breaking the proxy's ability to route requests correctly
var ProtectedParams = []string{"model"}

// Filters contains filter settings for modifying request parameters
// Used by both models and peers
type Filters struct {
	// StripParams is a comma-separated list of parameters to remove from requests
	// The "model" parameter can never be removed
	StripParams string `yaml:"stripParams"`

	// SetParams is a dictionary of parameters to set/override in requests
	// Protected params (like "model") cannot be set
	SetParams map[string]any `yaml:"setParams"`

	// SetParamsByID maps requested model IDs to parameters to set/override in requests.
	// Useful with aliases: a single loaded model can behave differently depending on
	// which alias the client used. Applied after SetParams, so it can override those values.
	// Protected params (like "model") cannot be set.
	SetParamsByID map[string]map[string]any `yaml:"setParamsByID"`

	// SetParamsByMatch applies parameters when a request field equals a value.
	// Rules are evaluated in order and every matching rule applies, so a later
	// rule overrides an earlier one. Applied after StripParams and before
	// SetParams/SetParamsByID, which keep the final say.
	SetParamsByMatch []MatchRule `yaml:"setParamsByMatch"`
}

// MatchRule sets request parameters when a request field equals a specific
// value. Unlike SetParamsByID, which keys off the requested model ID, a rule
// matches on the contents of the request body. Clients can therefore vary
// parameters between requests without changing the model they ask for, so no
// model swap is triggered and the loaded process is reused.
type MatchRule struct {
	// Key is the top-level request field to inspect (e.g. "reasoning_effort").
	Key string `yaml:"key"`

	// Match is the value Key must equal for the rule to apply. Non-string JSON
	// values are compared by their string form, so YAML booleans and numbers
	// must be quoted to match them.
	Match string `yaml:"match"`

	// Set holds the parameters applied when the rule matches. A value that is
	// a map is merged into an existing request object rather than replacing it.
	// Protected params (like "model") cannot be set.
	Set map[string]any `yaml:"set"`
}

// matchRuleKeyRegex restricts a rule key to characters gjson treats literally,
// so a lookup always targets the same top-level field.
var matchRuleKeyRegex = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Validate checks a single match rule at config load time.
func (r MatchRule) Validate() error {
	if r.Key == "" {
		return fmt.Errorf("key must not be empty")
	}
	if slices.Contains(ProtectedParams, r.Key) {
		return fmt.Errorf("key '%s' is a protected parameter", r.Key)
	}
	if !matchRuleKeyRegex.MatchString(r.Key) {
		return fmt.Errorf("key '%s' must contain only letters, digits, underscores, or hyphens", r.Key)
	}
	if len(r.Set) == 0 {
		return fmt.Errorf("set must not be empty")
	}
	return nil
}

// SanitizedSet returns the rule's params with protected params removed and keys
// sorted for consistent iteration order. Mirrors SanitizedSetParams.
func (r MatchRule) SanitizedSet() (map[string]any, []string) {
	if len(r.Set) == 0 {
		return nil, nil
	}

	result := make(map[string]any, len(r.Set))
	keys := make([]string, 0, len(r.Set))

	for key, value := range r.Set {
		if slices.Contains(ProtectedParams, key) {
			continue
		}
		result[key] = value
		keys = append(keys, key)
	}

	sort.Strings(keys)

	if len(result) == 0 {
		return nil, nil
	}

	return result, keys
}

// SanitizedStripParams returns a sorted list of parameters to strip,
// with duplicates, empty strings, and protected params removed
func (f Filters) SanitizedStripParams() []string {
	if f.StripParams == "" {
		return nil
	}

	params := strings.Split(f.StripParams, ",")
	cleaned := make([]string, 0, len(params))
	seen := make(map[string]bool)

	for _, param := range params {
		trimmed := strings.TrimSpace(param)
		// Skip protected params, empty strings, and duplicates
		if slices.Contains(ProtectedParams, trimmed) || trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		cleaned = append(cleaned, trimmed)
	}

	if len(cleaned) == 0 {
		return nil
	}

	slices.Sort(cleaned)
	return cleaned
}

// SanitizedSetParamsByID returns the params to set for the given requestedModelID,
// with protected params removed and keys sorted for consistent iteration order.
// Returns nil if the ID has no entry or all its params are protected.
func (f Filters) SanitizedSetParamsByID(requestedModelID string) (map[string]any, []string) {
	if len(f.SetParamsByID) == 0 {
		return nil, nil
	}
	params, found := f.SetParamsByID[requestedModelID]
	if !found || len(params) == 0 {
		return nil, nil
	}
	result := make(map[string]any, len(params))
	keys := make([]string, 0, len(params))
	for key, value := range params {
		if slices.Contains(ProtectedParams, key) {
			continue
		}
		result[key] = value
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(result) == 0 {
		return nil, nil
	}
	return result, keys
}

// SanitizedSetParams returns a copy of SetParams with protected params removed
// and keys sorted for consistent iteration order
func (f Filters) SanitizedSetParams() (map[string]any, []string) {
	if len(f.SetParams) == 0 {
		return nil, nil
	}

	result := make(map[string]any, len(f.SetParams))
	keys := make([]string, 0, len(f.SetParams))

	for key, value := range f.SetParams {
		// Skip protected params
		if slices.Contains(ProtectedParams, key) {
			continue
		}
		result[key] = value
		keys = append(keys, key)
	}

	// Sort keys for consistent ordering
	sort.Strings(keys)

	if len(result) == 0 {
		return nil, nil
	}

	return result, keys
}
