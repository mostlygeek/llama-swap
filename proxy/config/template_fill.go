package config

import (
	"fmt"
	"regexp"
	"strings"
)

// argPattern matches command line arguments like --arg, -a, or --arg=value
var argPattern = regexp.MustCompile(`^(-{1,2}[a-zA-Z][a-zA-Z0-9_-]*)(?:=(.*))?$`)

// ExpandVariantsResult holds the expanded models and a map from template ID to generated variant IDs.
// TemplateToVariants is only set for models that had variants; use it to substitute template IDs in groups/preload.
type ExpandVariantsResult struct {
	Models             map[string]ModelConfig
	TemplateToVariants map[string][]string // template ID -> list of variant model IDs
}

// ExpandVariants processes all models with variants and expands them into individual model configurations.
// Returns a new models map and a mapping from template ID to variant IDs. Fails fast on duplicate model ID.
func ExpandVariants(models map[string]ModelConfig) (ExpandVariantsResult, error) {
	result := make(map[string]ModelConfig)
	templateToVariants := make(map[string][]string)

	for modelID, modelConfig := range models {
		if len(modelConfig.Variants) == 0 {
			if _, exists := result[modelID]; exists {
				return ExpandVariantsResult{}, fmt.Errorf("duplicate model ID after expansion: %s", modelID)
			}
			result[modelID] = modelConfig
			continue
		}

		var variantIDs []string
		for variantSuffix, variantConfig := range modelConfig.Variants {
			expandedModel := expandVariant(modelConfig, variantSuffix, variantConfig)
			variantModelID := modelID + "-" + variantSuffix
			if _, exists := result[variantModelID]; exists {
				return ExpandVariantsResult{}, fmt.Errorf(
					"variant %q for model %q collides with existing model ID %q",
					variantSuffix, modelID, variantModelID,
				)
			}
			result[variantModelID] = expandedModel
			variantIDs = append(variantIDs, variantModelID)
		}
		templateToVariants[modelID] = variantIDs
	}

	return ExpandVariantsResult{Models: result, TemplateToVariants: templateToVariants}, nil
}

// expandVariant creates a new ModelConfig by applying variant overrides to the base model
func expandVariant(base ModelConfig, suffix string, variant VariantConfig) ModelConfig {
	expanded := ModelConfig{
		Cmd:              mergeCommands(base.Cmd, variant.CmdAdd),
		CmdStop:          base.CmdStop,
		Proxy:            base.Proxy,
		Aliases:          nil, // variants don't inherit base aliases to avoid duplicates
		Env:              copyStringSlice(base.Env),
		CheckEndpoint:    base.CheckEndpoint,
		UnloadAfter:      base.UnloadAfter,
		Unlisted:         base.Unlisted,
		UseModelName:     base.UseModelName,
		Name:             base.Name,
		Description:      base.Description,
		ConcurrencyLimit: base.ConcurrencyLimit,
		Filters:          base.Filters,
		Macros:           copyMacroList(base.Macros),
		Metadata:         copyMetadata(base.Metadata),
		SendLoadingState: base.SendLoadingState,
		Variants:         nil, // variants should not be copied to expanded models
	}

	// Apply variant overrides
	if variant.Name != "" {
		expanded.Name = variant.Name
	}

	if variant.Description != "" {
		expanded.Description = variant.Description
	}

	if len(variant.Env) > 0 {
		expanded.Env = append(expanded.Env, variant.Env...)
	}

	// Variants only get their own aliases, not inherited from base
	if len(variant.Aliases) > 0 {
		expanded.Aliases = copyStringSlice(variant.Aliases)
	}

	if variant.Unlisted != nil {
		expanded.Unlisted = *variant.Unlisted
	}

	return expanded
}

// mergeCommands merges the base command with additional arguments from the variant.
// Arguments in cmdAdd can override arguments in baseCmd if they have the same flag name.
func mergeCommands(baseCmd, cmdAdd string) string {
	if cmdAdd == "" {
		return baseCmd
	}

	baseCmd = strings.TrimSpace(baseCmd)
	cmdAdd = strings.TrimSpace(cmdAdd)

	if baseCmd == "" {
		return cmdAdd
	}

	// Parse base command into tokens
	baseTokens := tokenizeCommand(baseCmd)
	addTokens := tokenizeCommand(cmdAdd)

	// Build a map of argument positions in baseTokens for override detection
	// Key: normalized flag name (without leading dashes), Value: index in baseTokens
	baseArgIndices := make(map[string]int)
	for i := 0; i < len(baseTokens); i++ {
		token := baseTokens[i]
		if flag, _, isArg := parseArgument(token); isArg {
			baseArgIndices[normalizeFlag(flag)] = i
		}
	}

	// Process addTokens and either override existing args or append new ones
	var appendTokens []string
	i := 0
	for i < len(addTokens) {
		token := addTokens[i]
		flag, embeddedValue, isArg := parseArgument(token)

		if !isArg {
			// Not an argument, just append
			appendTokens = append(appendTokens, token)
			i++
			continue
		}

		normalizedFlag := normalizeFlag(flag)

		// Check if this argument exists in base
		if baseIdx, exists := baseArgIndices[normalizedFlag]; exists {
			// Override existing argument
			if embeddedValue != "" {
				// --arg=value format: replace base token; clear base's separate value if present
				baseTokens[baseIdx] = token
				if baseIdx+1 < len(baseTokens) && !isArgument(baseTokens[baseIdx+1]) {
					baseTokens[baseIdx+1] = ""
				}
				i++
			} else if i+1 < len(addTokens) && !isArgument(addTokens[i+1]) {
				// --arg value format (separate value)
				baseTokens[baseIdx] = token
				// Check if base also had a separate value
				if baseIdx+1 < len(baseTokens) && !isArgument(baseTokens[baseIdx+1]) {
					baseTokens[baseIdx+1] = addTokens[i+1]
				} else {
					// Base didn't have separate value, need to insert
					// For simplicity, use --flag=value format
					baseTokens[baseIdx] = flag + "=" + addTokens[i+1]
				}
				i += 2
			} else {
				// Boolean flag
				baseTokens[baseIdx] = token
				i++
			}
		} else {
			// New argument, append
			if embeddedValue != "" {
				appendTokens = append(appendTokens, token)
				i++
			} else if i+1 < len(addTokens) && !isArgument(addTokens[i+1]) {
				appendTokens = append(appendTokens, token, addTokens[i+1])
				i += 2
			} else {
				appendTokens = append(appendTokens, token)
				i++
			}
		}
	}

	// Reconstruct the command: drop empty slots (stale values from overrides) then join
	compact := make([]string, 0, len(baseTokens))
	for _, tok := range baseTokens {
		if tok != "" {
			compact = append(compact, tok)
		}
	}
	result := strings.Join(compact, " ")
	if len(appendTokens) > 0 {
		result += " " + strings.Join(appendTokens, " ")
	}

	return result
}

// tokenizeCommand splits a command string into tokens, handling quoted strings.
// Inside single- or double-quoted segments, backslash escape sequences are supported:
// \\ → \, \" → ", \' → ', \n → newline, \t → tab; any other \X is passed through as X.
// This allows values like --chat-template-kwargs "{\"enable_thinking\": false}" to parse correctly.
func tokenizeCommand(cmd string) []string {
	runes := []rune(cmd)
	var tokens []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case !inQuote && (r == '"' || r == '\''):
			inQuote = true
			quoteChar = r
			current.WriteRune(r)
		case inQuote && r == '\\' && i+1 < len(runes):
			next := runes[i+1]
			i++
			switch next {
			case '\\':
				current.WriteRune('\\')
			case '"':
				current.WriteRune('"')
			case '\'':
				current.WriteRune('\'')
			case 'n':
				current.WriteRune('\n')
			case 't':
				current.WriteRune('\t')
			default:
				current.WriteRune(next)
			}
		case inQuote && r == quoteChar:
			inQuote = false
			current.WriteRune(r)
			quoteChar = 0
		case !inQuote && (r == ' ' || r == '\t' || r == '\n'):
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// parseArgument checks if a token is a command line argument and extracts its components
// Returns: flag name (with dashes), embedded value (if --flag=value), isArgument bool
func parseArgument(token string) (flag string, value string, isArg bool) {
	matches := argPattern.FindStringSubmatch(token)
	if matches == nil {
		return "", "", false
	}
	return matches[1], matches[2], true
}

// isArgument checks if a token looks like a command line argument
func isArgument(token string) bool {
	_, _, isArg := parseArgument(token)
	return isArg
}

// normalizeFlag removes leading dashes and converts to lowercase for comparison
func normalizeFlag(flag string) string {
	flag = strings.TrimLeft(flag, "-")
	return strings.ToLower(flag)
}

// copyMacroList creates a copy of MacroList so expanded variants do not share the slice with the base.
func copyMacroList(ml MacroList) MacroList {
	if ml == nil {
		return nil
	}
	result := make(MacroList, len(ml))
	copy(result, ml)
	return result
}

// copyStringSlice creates a copy of a string slice
func copyStringSlice(s []string) []string {
	if s == nil {
		return nil
	}
	result := make([]string, len(s))
	copy(result, s)
	return result
}

// copyMetadata creates a shallow copy of metadata map
func copyMetadata(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
