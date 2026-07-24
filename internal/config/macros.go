package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	macroNameRegex    = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	macroPatternRegex = regexp.MustCompile(`\$\{([a-zA-Z0-9_-]+)\}`)
	envMacroRegex     = regexp.MustCompile(`\$\{env\.([a-zA-Z_][a-zA-Z0-9_]*)\}`)
)

// validateMacro validates macro name and value constraints
func validateMacro(name string, value any) error {
	if len(name) >= 64 {
		return fmt.Errorf("macro name '%s' exceeds maximum length of 63 characters", name)
	}
	if !macroNameRegex.MatchString(name) {
		return fmt.Errorf("macro name '%s' contains invalid characters, must match pattern ^[a-zA-Z0-9_-]+$", name)
	}

	// Validate that value is a scalar type
	switch v := value.(type) {
	case string:
		// Check for self-reference
		macroSlug := fmt.Sprintf("${%s}", name)
		if strings.Contains(v, macroSlug) {
			return fmt.Errorf("macro '%s' contains self-reference", name)
		}
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool:
		// These types are allowed
	default:
		return fmt.Errorf("macro '%s' has invalid type %T, must be a scalar type (string, int, float, or bool)", name, value)
	}

	switch name {
	case "PORT", "MODEL_ID":
		return fmt.Errorf("macro name '%s' is reserved", name)
	}

	return nil
}

// validateNestedForUnknownMacros recursively checks for any remaining macro references in nested structures
func validateNestedForUnknownMacros(value any, context string) error {
	switch v := value.(type) {
	case string:
		matches := macroPatternRegex.FindAllStringSubmatch(v, -1)
		for _, match := range matches {
			macroName := match[1]
			return fmt.Errorf("%s: unknown macro '${%s}'", context, macroName)
		}
		// Check for unsubstituted env macros
		envMatches := envMacroRegex.FindAllStringSubmatch(v, -1)
		for _, match := range envMatches {
			varName := match[1]
			return fmt.Errorf("%s: environment variable '%s' not set", context, varName)
		}
		return nil

	case map[string]any:
		for _, val := range v {
			if err := validateNestedForUnknownMacros(val, context); err != nil {
				return err
			}
		}
		return nil

	case []any:
		for _, val := range v {
			if err := validateNestedForUnknownMacros(val, context); err != nil {
				return err
			}
		}
		return nil

	default:
		// Scalar types don't contain macros
		return nil
	}
}

// substituteMacroInValue recursively substitutes a single macro in a value structure
// This is called once per macro, allowing LIFO substitution order
func substituteMacroInValue(value any, macroName string, macroValue any) (any, error) {
	macroSlug := fmt.Sprintf("${%s}", macroName)
	macroStr := fmt.Sprintf("%v", macroValue)

	switch v := value.(type) {
	case string:
		// Check if this is a direct macro substitution
		if v == macroSlug {
			return macroValue, nil
		}
		// Handle string interpolation
		if strings.Contains(v, macroSlug) {
			return strings.ReplaceAll(v, macroSlug, macroStr), nil
		}
		return v, nil

	case map[string]any:
		// Recursively process map values
		newMap := make(map[string]any)
		for key, val := range v {
			newVal, err := substituteMacroInValue(val, macroName, macroValue)
			if err != nil {
				return nil, err
			}
			newMap[key] = newVal
		}
		return newMap, nil

	case []any:
		// Recursively process slice elements
		newSlice := make([]any, len(v))
		for i, val := range v {
			newVal, err := substituteMacroInValue(val, macroName, macroValue)
			if err != nil {
				return nil, err
			}
			newSlice[i] = newVal
		}
		return newSlice, nil

	default:
		// Return scalar types as-is
		return value, nil
	}
}

// substituteEnvMacros replaces ${env.VAR_NAME} with environment variable values.
// Returns error if any referenced env var is not set or contains invalid characters.
// Env macros inside YAML comments are ignored by unmarshalling the YAML first
// (which strips comments) and only checking the comment-free version for macros.
func substituteEnvMacros(s string) (string, error) {
	// Unmarshal and remarshal to strip YAML comments
	var raw any
	if err := yaml.Unmarshal([]byte(s), &raw); err != nil {
		// If YAML is invalid, fall back to scanning the original string
		// so the user gets the env var error rather than a confusing YAML parse error
		return substituteEnvMacrosInString(s, s)
	}
	clean, err := yaml.Marshal(raw)
	if err != nil {
		return substituteEnvMacrosInString(s, s)
	}

	return substituteEnvMacrosInString(s, string(clean))
}

// substituteEnvMacrosInString finds ${env.VAR} macros in scanStr and substitutes
// them in target. This separation allows scanning comment-free YAML while
// substituting in the original string.
func substituteEnvMacrosInString(target, scanStr string) (string, error) {
	result := target
	matches := envMacroRegex.FindAllStringSubmatch(scanStr, -1)
	for _, match := range matches {
		fullMatch := match[0] // ${env.VAR_NAME}
		varName := match[1]   // VAR_NAME

		value, exists := os.LookupEnv(varName)
		if !exists {
			return "", fmt.Errorf("environment variable '%s' is not set", varName)
		}

		// Sanitize the value for safe YAML substitution
		value, err := sanitizeEnvValueForYAML(value, varName)
		if err != nil {
			return "", err
		}

		result = strings.ReplaceAll(result, fullMatch, value)
	}
	return result, nil
}

// sanitizeEnvValueForYAML ensures an environment variable value is safe for YAML substitution.
// It rejects values with characters that break YAML structure and escapes quotes/backslashes
// for compatibility with double-quoted YAML strings.
func sanitizeEnvValueForYAML(value, varName string) (string, error) {
	// Reject values that would break YAML structure regardless of quoting context
	if strings.ContainsAny(value, "\n\r\x00") {
		return "", fmt.Errorf("environment variable '%s' contains newlines or null bytes which are not allowed in YAML substitution", varName)
	}

	// Escape backslashes and double quotes for safe use in double-quoted YAML strings.
	// In unquoted contexts, these escapes appear literally (harmless for most use cases).
	// In double-quoted contexts, they are interpreted correctly.
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)

	return value, nil
}
