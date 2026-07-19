package config

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
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

	// Reasoning translates a client-sent reasoning effort field (default
	// "reasoning_effort") into llama.cpp-native request fields. Applied after
	// StripParams and before SetParams; a preset never overwrites fields the
	// client set explicitly.
	Reasoning *ReasoningFilter `yaml:"reasoning"`
}

// DefaultReasoningInputField is the request field consulted by ReasoningFilter
// when inputField is not configured.
const DefaultReasoningInputField = "reasoning_effort"

// ReasoningFilter maps client reasoning effort values (e.g. "none", "medium")
// to llama.cpp-native request parameters. Only string-valued input fields are
// translated; unknown or non-string values are forwarded unchanged.
//
// The custom UnmarshalYAML stores an untyped representation so presets can
// reference a map-valued macro (presets: ${my_presets}), which is not
// decodable into the typed field. After ResolveMacros is called the typed
// fields (InputField, Presets) are populated.
type ReasoningFilter struct {
	// InputField is the top-level request field holding the effort value.
	// Defaults to "reasoning_effort".
	InputField string `yaml:"inputField"`

	// Presets maps an effort value to the native fields to inject.
	Presets map[string]ReasoningPreset `yaml:"presets"`

	raw map[string]any
}

// UnmarshalYAML decodes the reasoning filter into an untyped map. This
// materializes YAML aliases while allowing macro placeholders in fields that
// will ultimately be decoded as structured values (e.g. presets).
func (rf *ReasoningFilter) UnmarshalYAML(value *yaml.Node) error {
	return value.Decode(&rf.raw)
}

// ResolveMacros substitutes all macros in the untyped representation (LIFO
// order matching LoadConfigFromReader), then decodes the resolved values into
// the typed fields. It is a no-op for programmatically constructed filters.
func (rf *ReasoningFilter) ResolveMacros(macros MacroList) error {
	if rf.raw == nil {
		return nil
	}

	var resolved any = rf.raw
	for i := len(macros) - 1; i >= 0; i-- {
		entry := macros[i]
		var err error
		resolved, err = substituteMacroInValue(resolved, entry.Name, entry.Value)
		if err != nil {
			return fmt.Errorf("failed macro substitution: %w", err)
		}
	}
	if err := validateNestedForUnknownMacros(resolved, "presets"); err != nil {
		return err
	}

	var node yaml.Node
	if err := node.Encode(resolved); err != nil {
		return fmt.Errorf("failed to encode after macro substitution: %w", err)
	}
	type rawReasoningFilter ReasoningFilter
	typed := rawReasoningFilter{}
	if err := node.Decode(&typed); err != nil {
		return fmt.Errorf("failed to decode after macro substitution: %w", err)
	}
	rf.InputField = typed.InputField
	rf.Presets = typed.Presets

	return nil
}

// ReasoningPreset describes the llama.cpp-native fields injected for one
// effort value. Fields left nil are not injected at all.
type ReasoningPreset struct {
	// EnableThinking sets chat_template_kwargs.enable_thinking.
	EnableThinking *bool `yaml:"enableThinking"`

	// BudgetTokens sets the top-level thinking_budget_tokens. Omitting it
	// means no thinking_budget_tokens field is injected.
	BudgetTokens *int `yaml:"budgetTokens"`
}

// ReasoningInputField returns the request field the reasoning filter reads,
// or "" when no reasoning filter is configured.
func (f Filters) ReasoningInputField() string {
	if f.Reasoning == nil {
		return ""
	}
	if f.Reasoning.InputField == "" {
		return DefaultReasoningInputField
	}
	return f.Reasoning.InputField
}

// PresetFor returns the preset for the given effort value.
func (rf *ReasoningFilter) PresetFor(effort string) (ReasoningPreset, bool) {
	preset, found := rf.Presets[effort]
	return preset, found
}

// reasoningInputFieldRegex restricts inputField to characters that gjson/sjson
// treat literally, so lookups and deletes always target the same top-level key.
var reasoningInputFieldRegex = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Validate checks the reasoning filter configuration at load time.
func (rf *ReasoningFilter) Validate() error {
	if slices.Contains(ProtectedParams, rf.InputField) {
		return fmt.Errorf("inputField '%s' is a protected parameter", rf.InputField)
	}
	if rf.InputField != "" && !reasoningInputFieldRegex.MatchString(rf.InputField) {
		return fmt.Errorf("inputField '%s' must contain only letters, digits, underscores, or hyphens", rf.InputField)
	}
	if len(rf.Presets) == 0 {
		return fmt.Errorf("presets must not be empty")
	}
	for effort, preset := range rf.Presets {
		if preset.EnableThinking == nil && preset.BudgetTokens == nil {
			return fmt.Errorf("preset '%s' must set enableThinking and/or budgetTokens", effort)
		}
		if preset.BudgetTokens != nil && *preset.BudgetTokens <= 0 {
			return fmt.Errorf("preset '%s' budgetTokens must be greater than 0", effort)
		}
	}
	return nil
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
