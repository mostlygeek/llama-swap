package config

import (
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

	// SetParams is a dictionary of parameters to set/override in requests.
	// A key ending in "?" (e.g. "max_tokens?") is set-if-undefined: it is only
	// applied when the request does not already carry that parameter.
	// Protected params (like "model") cannot be set
	SetParams map[string]any `yaml:"setParams"`

	// SetParamsByID maps requested model IDs to parameters to set/override in requests.
	// Useful with aliases: a single loaded model can behave differently depending on
	// which alias the client used. Applied after SetParams, so it can override those values.
	// Keys ending in "?" are set-if-undefined, as in SetParams.
	// Protected params (like "model") cannot be set.
	SetParamsByID map[string]map[string]any `yaml:"setParamsByID"`
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
// with protected params removed, the "?" set-if-undefined suffix stripped from
// keys, and keys sorted for consistent iteration order. Keys spelled with the
// suffix are reported in soft. Returns nil if the ID has no entry or all its
// params are protected.
func (f Filters) SanitizedSetParamsByID(requestedModelID string) (map[string]any, []string, map[string]bool) {
	if len(f.SetParamsByID) == 0 {
		return nil, nil, nil
	}
	params, found := f.SetParamsByID[requestedModelID]
	if !found || len(params) == 0 {
		return nil, nil, nil
	}
	return sanitizeParams(params)
}

// SanitizedSetParams returns a copy of SetParams with protected params removed,
// the "?" set-if-undefined suffix stripped from keys, and keys sorted for
// consistent iteration order. Keys spelled with the suffix are reported in soft.
func (f Filters) SanitizedSetParams() (map[string]any, []string, map[string]bool) {
	if len(f.SetParams) == 0 {
		return nil, nil, nil
	}
	return sanitizeParams(f.SetParams)
}

// sanitizeParams removes protected params from raw and strips the "?" suffix
// that marks a set-if-undefined key (issue #1052), e.g. "max_tokens?": 32768.
// Such keys are returned (without the suffix) in soft; callers apply them only
// when the body does not already carry the parameter. When the same key is
// spelled both hard and soft, the hard spelling wins. Keys are sorted for
// consistent iteration order.
func sanitizeParams(raw map[string]any) (map[string]any, []string, map[string]bool) {
	result := make(map[string]any, len(raw))
	soft := make(map[string]bool)

	// Hard keys first, so "key" wins over "key?"
	for key, value := range raw {
		if strings.HasSuffix(key, "?") || slices.Contains(ProtectedParams, key) {
			continue
		}
		result[key] = value
	}

	for key, value := range raw {
		if !strings.HasSuffix(key, "?") {
			continue
		}
		key = strings.TrimSuffix(key, "?")
		if key == "" || slices.Contains(ProtectedParams, key) {
			continue
		}
		if _, isHard := result[key]; isHard {
			continue
		}
		result[key] = value
		soft[key] = true
	}

	if len(result) == 0 {
		return nil, nil, nil
	}
	if len(soft) == 0 {
		soft = nil
	}

	keys := make([]string, 0, len(result))
	for key := range result {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return result, keys, soft
}
