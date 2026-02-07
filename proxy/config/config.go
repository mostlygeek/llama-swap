package config

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/billziss-gh/golib/shlex"
	"gopkg.in/yaml.v3"
)

const DEFAULT_GROUP_ID = "(default)"
const (
	LogToStdoutProxy    = "proxy"
	LogToStdoutUpstream = "upstream"
	LogToStdoutBoth     = "both"
	LogToStdoutNone     = "none"
)

type MacroEntry struct {
	Name  string
	Value any
}

type MacroList []MacroEntry

// UnmarshalYAML implements custom YAML unmarshaling that preserves macro definition order
func (ml *MacroList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("macros must be a mapping")
	}

	// yaml.Node.Content for a mapping contains alternating key/value nodes
	entries := make([]MacroEntry, 0, len(value.Content)/2)
	for i := 0; i < len(value.Content); i += 2 {
		keyNode := value.Content[i]
		valueNode := value.Content[i+1]

		var name string
		if err := keyNode.Decode(&name); err != nil {
			return fmt.Errorf("failed to decode macro name: %w", err)
		}

		var val any
		if err := valueNode.Decode(&val); err != nil {
			return fmt.Errorf("failed to decode macro value for '%s': %w", name, err)
		}

		entries = append(entries, MacroEntry{Name: name, Value: val})
	}

	*ml = entries
	return nil
}

// Get retrieves a macro value by name
func (ml MacroList) Get(name string) (any, bool) {
	for _, entry := range ml {
		if entry.Name == name {
			return entry.Value, true
		}
	}
	return nil, false
}

// ToMap converts MacroList to a map (for backward compatibility if needed)
func (ml MacroList) ToMap() map[string]any {
	result := make(map[string]any, len(ml))
	for _, entry := range ml {
		result[entry.Name] = entry.Value
	}
	return result
}

type GroupConfig struct {
	Swap       bool     `yaml:"swap"`
	Exclusive  bool     `yaml:"exclusive"`
	Persistent bool     `yaml:"persistent"`
	Members    []string `yaml:"members"`
}

var (
	macroNameRegex    = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	macroPatternRegex = regexp.MustCompile(`\$\{([a-zA-Z0-9_-]+)\}`)
	envMacroRegex     = regexp.MustCompile(`\$\{env\.([a-zA-Z_][a-zA-Z0-9_]*)\}`)
)

// set default values for GroupConfig
func (c *GroupConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawGroupConfig GroupConfig
	defaults := rawGroupConfig{
		Swap:       true,
		Exclusive:  true,
		Persistent: false,
		Members:    []string{},
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	*c = GroupConfig(defaults)
	return nil
}

type HooksConfig struct {
	OnStartup HookOnStartup `yaml:"on_startup"`
}

type HookOnStartup struct {
	Preload []string `yaml:"preload"`
}

type Config struct {
	HealthCheckTimeout int                    `yaml:"healthCheckTimeout"`
	LogRequests        bool                   `yaml:"logRequests"`
	LogLevel           string                 `yaml:"logLevel"`
	LogTimeFormat      string                 `yaml:"logTimeFormat"`
	LogToStdout        string                 `yaml:"logToStdout"`
	MetricsMaxInMemory int                    `yaml:"metricsMaxInMemory"`
	CaptureBuffer      int                    `yaml:"captureBuffer"`
	Models             map[string]ModelConfig `yaml:"models"` /* key is model ID */
	Profiles           map[string][]string    `yaml:"profiles"`
	Groups             map[string]GroupConfig `yaml:"groups"` /* key is group ID */

	// for key/value replacements in model's cmd, cmdStop, proxy, checkEndPoint
	Macros MacroList `yaml:"macros"`

	// map aliases to actual model IDs
	aliases map[string]string

	// automatic port assignments
	StartPort int `yaml:"startPort"`

	// hooks, see: #209
	Hooks HooksConfig `yaml:"hooks"`

	// send loading state in reasoning
	SendLoadingState bool `yaml:"sendLoadingState"`

	// present aliases to /v1/models OpenAI API listing
	IncludeAliasesInList bool `yaml:"includeAliasesInList"`

	// support API keys, see issue #433, #50, #251
	RequiredAPIKeys []string `yaml:"apiKeys"`

	// support remote peers, see issue #433, #296
	Peers PeerDictionaryConfig `yaml:"peers"`
}

func (c *Config) RealModelName(search string) (string, bool) {
	if _, found := c.Models[search]; found {
		return search, true
	} else if name, found := c.aliases[search]; found {
		return name, found
	} else {
		return "", false
	}
}

func (c *Config) FindConfig(modelName string) (ModelConfig, string, bool) {
	if realName, found := c.RealModelName(modelName); !found {
		return ModelConfig{}, "", false
	} else {
		return c.Models[realName], realName, true
	}
}

func LoadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	return LoadConfigFromReader(file)
}

func LoadConfigFromReader(r io.Reader) (Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Config{}, err
	}
	yamlStr := string(data)

	// Phase 1: Substitute all ${env.VAR} macros at string level
	// This is safe because env values are simple strings without YAML formatting
	yamlStr, err = substituteEnvMacros(yamlStr)
	if err != nil {
		return Config{}, err
	}

	// Unmarshal into full Config with defaults
	config := Config{
		HealthCheckTimeout: 120,
		StartPort:          5800,
		LogLevel:           "info",
		LogTimeFormat:      "",
		LogToStdout:        LogToStdoutProxy,
		MetricsMaxInMemory: 1000,
		CaptureBuffer:      5,
	}
	if err = yaml.Unmarshal([]byte(yamlStr), &config); err != nil {
		return Config{}, err
	}

	if config.HealthCheckTimeout < 15 {
		config.HealthCheckTimeout = 15
	}

	if config.StartPort < 1 {
		return Config{}, fmt.Errorf("startPort must be greater than 1")
	}

	switch config.LogToStdout {
	case LogToStdoutProxy, LogToStdoutUpstream, LogToStdoutBoth, LogToStdoutNone:
	default:
		return Config{}, fmt.Errorf("logToStdout must be one of: proxy, upstream, both, none")
	}

	// Populate the aliases map
	config.aliases = make(map[string]string)
	for modelName, modelConfig := range config.Models {
		for _, alias := range modelConfig.Aliases {
			if _, found := config.aliases[alias]; found {
				return Config{}, fmt.Errorf("duplicate alias %s found in model: %s", alias, modelName)
			}
			config.aliases[alias] = modelName
		}
	}

	// Validate global macros
	for _, macro := range config.Macros {
		if err = validateMacro(macro.Name, macro.Value); err != nil {
			return Config{}, err
		}
	}

	// Get and sort all model IDs for consistent port assignment
	modelIds := make([]string, 0, len(config.Models))
	for modelId := range config.Models {
		modelIds = append(modelIds, modelId)
	}
	sort.Strings(modelIds)

	nextPort := config.StartPort
	for _, modelId := range modelIds {
		modelConfig := config.Models[modelId]

		// Strip comments from command fields
		modelConfig.Cmd = StripComments(modelConfig.Cmd)
		modelConfig.CmdStop = StripComments(modelConfig.CmdStop)

		// Validate model macros
		for _, macro := range modelConfig.Macros {
			if err = validateMacro(macro.Name, macro.Value); err != nil {
				return Config{}, fmt.Errorf("model %s: %s", modelId, err.Error())
			}
		}

		// Build merged macro list: MODEL_ID + global macros + model macros (model overrides global)
		mergedMacros := make(MacroList, 0, len(config.Macros)+len(modelConfig.Macros)+1)
		mergedMacros = append(mergedMacros, MacroEntry{Name: "MODEL_ID", Value: modelId})
		mergedMacros = append(mergedMacros, config.Macros...)

		// Add model macros (override globals with same name)
		for _, entry := range modelConfig.Macros {
			found := false
			for i, existing := range mergedMacros {
				if existing.Name == entry.Name {
					mergedMacros[i] = entry
					found = true
					break
				}
			}
			if !found {
				mergedMacros = append(mergedMacros, entry)
			}
		}

		// Substitute remaining macros in model fields (LIFO order)
		for i := len(mergedMacros) - 1; i >= 0; i-- {
			entry := mergedMacros[i]
			macroSlug := fmt.Sprintf("${%s}", entry.Name)
			macroStr := fmt.Sprintf("%v", entry.Value)

			modelConfig.Cmd = strings.ReplaceAll(modelConfig.Cmd, macroSlug, macroStr)
			modelConfig.CmdStop = strings.ReplaceAll(modelConfig.CmdStop, macroSlug, macroStr)
			modelConfig.Proxy = strings.ReplaceAll(modelConfig.Proxy, macroSlug, macroStr)
			modelConfig.CheckEndpoint = strings.ReplaceAll(modelConfig.CheckEndpoint, macroSlug, macroStr)
			modelConfig.Filters.StripParams = strings.ReplaceAll(modelConfig.Filters.StripParams, macroSlug, macroStr)

			// Substitute in metadata (type-preserving)
			if len(modelConfig.Metadata) > 0 {
				result, err := substituteMacroInValue(modelConfig.Metadata, entry.Name, entry.Value)
				if err != nil {
					return Config{}, fmt.Errorf("model %s metadata: %s", modelId, err.Error())
				}
				modelConfig.Metadata = result.(map[string]any)
			}
		}

		// Handle PORT macro - only allocate if cmd uses it
		cmdHasPort := strings.Contains(modelConfig.Cmd, "${PORT}")
		proxyHasPort := strings.Contains(modelConfig.Proxy, "${PORT}")
		if cmdHasPort || proxyHasPort {
			if !cmdHasPort && proxyHasPort {
				return Config{}, fmt.Errorf("model %s: proxy uses ${PORT} but cmd does not - ${PORT} is only available when used in cmd", modelId)
			}

			macroSlug := "${PORT}"
			macroStr := fmt.Sprintf("%v", nextPort)

			modelConfig.Cmd = strings.ReplaceAll(modelConfig.Cmd, macroSlug, macroStr)
			modelConfig.CmdStop = strings.ReplaceAll(modelConfig.CmdStop, macroSlug, macroStr)
			modelConfig.Proxy = strings.ReplaceAll(modelConfig.Proxy, macroSlug, macroStr)

			if len(modelConfig.Metadata) > 0 {
				result, err := substituteMacroInValue(modelConfig.Metadata, "PORT", nextPort)
				if err != nil {
					return Config{}, fmt.Errorf("model %s metadata: %s", modelId, err.Error())
				}
				modelConfig.Metadata = result.(map[string]any)
			}

			nextPort++
		}

		// Validate no unknown macros remain
		fieldMap := map[string]string{
			"cmd":                 modelConfig.Cmd,
			"cmdStop":             modelConfig.CmdStop,
			"proxy":               modelConfig.Proxy,
			"checkEndpoint":       modelConfig.CheckEndpoint,
			"filters.stripParams": modelConfig.Filters.StripParams,
		}

		for fieldName, fieldValue := range fieldMap {
			matches := macroPatternRegex.FindAllStringSubmatch(fieldValue, -1)
			for _, match := range matches {
				macroName := match[1]
				if macroName == "PID" && fieldName == "cmdStop" {
					continue // replaced at runtime
				}
				if macroName == "PORT" || macroName == "MODEL_ID" {
					return Config{}, fmt.Errorf("macro '${%s}' should have been substituted in %s.%s", macroName, modelId, fieldName)
				}
				return Config{}, fmt.Errorf("unknown macro '${%s}' found in %s.%s", macroName, modelId, fieldName)
			}
		}

		if len(modelConfig.Metadata) > 0 {
			if err := validateNestedForUnknownMacros(modelConfig.Metadata, fmt.Sprintf("model %s metadata", modelId)); err != nil {
				return Config{}, err
			}
		}

		if _, err := url.Parse(modelConfig.Proxy); err != nil {
			return Config{}, fmt.Errorf("model %s: invalid proxy URL: %w", modelId, err)
		}

		if modelConfig.SendLoadingState == nil {
			v := config.SendLoadingState
			modelConfig.SendLoadingState = &v
		}

		config.Models[modelId] = modelConfig
	}

	config = AddDefaultGroupToConfig(config)

	// Validate group members
	memberUsage := make(map[string]string)
	for groupID, groupConfig := range config.Groups {
		prevSet := make(map[string]bool)
		for _, member := range groupConfig.Members {
			if _, found := prevSet[member]; found {
				return Config{}, fmt.Errorf("duplicate model member %s found in group: %s", member, groupID)
			}
			prevSet[member] = true

			if existingGroup, exists := memberUsage[member]; exists {
				return Config{}, fmt.Errorf("model member %s is used in multiple groups: %s and %s", member, existingGroup, groupID)
			}
			memberUsage[member] = groupID
		}
	}

	// Clean up hooks preload
	if len(config.Hooks.OnStartup.Preload) > 0 {
		var toPreload []string
		for _, modelID := range config.Hooks.OnStartup.Preload {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				continue
			}
			if real, found := config.RealModelName(modelID); found {
				toPreload = append(toPreload, real)
			}
		}
		config.Hooks.OnStartup.Preload = toPreload
	}

	// Validate API keys (env macros already substituted at string level)
	for i, apikey := range config.RequiredAPIKeys {
		if apikey == "" {
			return Config{}, fmt.Errorf("empty api key found in apiKeys")
		}
		if strings.Contains(apikey, " ") {
			return Config{}, fmt.Errorf("api key cannot contain spaces: `%s`", apikey)
		}
		config.RequiredAPIKeys[i] = apikey
	}

	// Process peers with global macro substitution
	for peerName, peerConfig := range config.Peers {
		// Substitute global macros (LIFO order)
		for i := len(config.Macros) - 1; i >= 0; i-- {
			entry := config.Macros[i]
			macroSlug := fmt.Sprintf("${%s}", entry.Name)
			macroStr := fmt.Sprintf("%v", entry.Value)

			peerConfig.ApiKey = strings.ReplaceAll(peerConfig.ApiKey, macroSlug, macroStr)
			peerConfig.Filters.StripParams = strings.ReplaceAll(peerConfig.Filters.StripParams, macroSlug, macroStr)

			// Substitute in setParams (type-preserving)
			if len(peerConfig.Filters.SetParams) > 0 {
				result, err := substituteMacroInValue(peerConfig.Filters.SetParams, entry.Name, entry.Value)
				if err != nil {
					return Config{}, fmt.Errorf("peers.%s.filters.setParams: %w", peerName, err)
				}
				peerConfig.Filters.SetParams = result.(map[string]any)
			}
		}

		// Validate no unknown macros remain
		if matches := macroPatternRegex.FindAllStringSubmatch(peerConfig.ApiKey, -1); len(matches) > 0 {
			return Config{}, fmt.Errorf("peers.%s.apiKey: unknown macro '${%s}'", peerName, matches[0][1])
		}
		if matches := macroPatternRegex.FindAllStringSubmatch(peerConfig.Filters.StripParams, -1); len(matches) > 0 {
			return Config{}, fmt.Errorf("peers.%s.filters.stripParams: unknown macro '${%s}'", peerName, matches[0][1])
		}
		if len(peerConfig.Filters.SetParams) > 0 {
			if err := validateNestedForUnknownMacros(peerConfig.Filters.SetParams, fmt.Sprintf("peers.%s.filters.setParams", peerName)); err != nil {
				return Config{}, err
			}
		}
		config.Peers[peerName] = peerConfig
	}

	return config, nil
}

// rewrites the yaml to include a default group with any orphaned models
func AddDefaultGroupToConfig(config Config) Config {

	if config.Groups == nil {
		config.Groups = make(map[string]GroupConfig)
	}

	defaultGroup := GroupConfig{
		Swap:      true,
		Exclusive: true,
		Members:   []string{},
	}
	// if groups is empty, create a default group and put
	// all models into it
	if len(config.Groups) == 0 {
		for modelName := range config.Models {
			defaultGroup.Members = append(defaultGroup.Members, modelName)
		}
	} else {
		// iterate over existing group members and add non-grouped models into the default group
		for modelName := range config.Models {
			foundModel := false
		found:
			// search for the model in existing groups
			for _, groupConfig := range config.Groups {
				for _, member := range groupConfig.Members {
					if member == modelName {
						foundModel = true
						break found
					}
				}
			}

			if !foundModel {
				defaultGroup.Members = append(defaultGroup.Members, modelName)
			}
		}
	}

	sort.Strings(defaultGroup.Members) // make consistent ordering for testing
	config.Groups[DEFAULT_GROUP_ID] = defaultGroup

	return config
}

func SanitizeCommand(cmdStr string) ([]string, error) {
	var cleanedLines []string
	for _, line := range strings.Split(cmdStr, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip comment lines
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Handle trailing backslashes by replacing with space
		if strings.HasSuffix(trimmed, "\\") {
			cleanedLines = append(cleanedLines, strings.TrimSuffix(trimmed, "\\")+" ")
		} else {
			cleanedLines = append(cleanedLines, line)
		}
	}

	// put it back together
	cmdStr = strings.Join(cleanedLines, "\n")

	// Split the command into arguments
	var args []string
	if runtime.GOOS == "windows" {
		args = shlex.Windows.Split(cmdStr)
	} else {
		args = shlex.Posix.Split(cmdStr)
	}

	// Ensure the command is not empty
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	return args, nil
}

func StripComments(cmdStr string) string {
	var cleanedLines []string
	for _, line := range strings.Split(cmdStr, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip comment lines
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		cleanedLines = append(cleanedLines, line)
	}
	return strings.Join(cleanedLines, "\n")
}

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
		if len(v) >= 1024 {
			return fmt.Errorf("macro value for '%s' exceeds maximum length of 1024 characters", name)
		}
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
