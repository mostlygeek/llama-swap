package config

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

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
		GlobalTTL:          0,
	}
	if err = yaml.Unmarshal([]byte(yamlStr), &config); err != nil {
		return Config{}, err
	}

	if config.HealthCheckTimeout < 15 {
		config.HealthCheckTimeout = 15
	}

	// Apply defaults for performance config when section is missing
	if config.Performance.Every == 0 {
		config.Performance.Every = 5 * time.Second
	}
	if err = config.Performance.Validate(); err != nil {
		return Config{}, fmt.Errorf("performance: %w", err)
	}

	if config.StartPort < 1 {
		return Config{}, fmt.Errorf("startPort must be greater than 1")
	}

	if config.GlobalTTL < 0 {
		return Config{}, fmt.Errorf("globalTTL must be >= 0")
	}

	// Apply default for upstream.ignorePaths when not specified. The default
	// matches common static-asset suffixes so they do not trigger a swap.
	if len(config.Upstream.IgnorePaths) == 0 {
		config.Upstream.IgnorePaths = DefaultUpstreamIgnorePaths()
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
		modelConfig.HealthCheckTimeout = config.HealthCheckTimeout

		// Strip comments from command fields
		modelConfig.Cmd = StripComments(modelConfig.Cmd)
		modelConfig.CmdStop = StripComments(modelConfig.CmdStop)

		// set model TTL to globalTTL it is the default value
		if modelConfig.UnloadAfter == MODEL_CONFIG_DEFAULT_TTL {
			modelConfig.UnloadAfter = config.GlobalTTL
		}

		if modelConfig.UnloadAfter < 0 {
			return Config{}, fmt.Errorf("model %s: invalid TTL value %d", modelId, modelConfig.UnloadAfter)
		}

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
			modelConfig.Name = strings.ReplaceAll(modelConfig.Name, macroSlug, macroStr)
			modelConfig.Description = strings.ReplaceAll(modelConfig.Description, macroSlug, macroStr)

			// Substitute macros in SetParamsByID keys and values
			if len(modelConfig.Filters.SetParamsByID) > 0 {
				newSetParamsByID := make(map[string]map[string]any, len(modelConfig.Filters.SetParamsByID))
				for key, paramMap := range modelConfig.Filters.SetParamsByID {
					newKey := strings.ReplaceAll(key, macroSlug, macroStr)
					newValAny, err := substituteMacroInValue(any(paramMap), entry.Name, entry.Value)
					if err != nil {
						return Config{}, fmt.Errorf("model %s filters.setParamsByID: %s", modelId, err.Error())
					}
					newParamMap, ok := newValAny.(map[string]any)
					if !ok {
						return Config{}, fmt.Errorf("model %s filters.setParamsByID: unexpected type after macro substitution", modelId)
					}
					newSetParamsByID[newKey] = newParamMap
				}
				modelConfig.Filters.SetParamsByID = newSetParamsByID
			}

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
			modelConfig.Name = strings.ReplaceAll(modelConfig.Name, macroSlug, macroStr)
			modelConfig.Description = strings.ReplaceAll(modelConfig.Description, macroSlug, macroStr)

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
			"name":                modelConfig.Name,
			"description":         modelConfig.Description,
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

		if err = modelConfig.Capabilities.Validate(); err != nil {
			return Config{}, fmt.Errorf("model %s: %w", modelId, err)
		}

		// Validate SetParamsByID keys and values
		for key, paramMap := range modelConfig.Filters.SetParamsByID {
			if matches := macroPatternRegex.FindAllStringSubmatch(key, -1); len(matches) > 0 {
				return Config{}, fmt.Errorf("unknown macro '${%s}' found in model %s filters.setParamsByID key", matches[0][1], modelId)
			}
			if err := validateNestedForUnknownMacros(any(paramMap), fmt.Sprintf("model %s filters.setParamsByID[%s]", modelId, key)); err != nil {
				return Config{}, err
			}
		}

		// Auto-register setParamsByID keys as aliases (skip the model's own ID)
		for key := range modelConfig.Filters.SetParamsByID {
			if key == modelId {
				continue
			}
			if _, exists := config.Models[key]; exists {
				return Config{}, fmt.Errorf("model %s filters.setParamsByID: key '%s' conflicts with an existing model ID", modelId, key)
			}
			if existingModel, exists := config.aliases[key]; exists {
				if existingModel != modelId {
					return Config{}, fmt.Errorf("duplicate alias '%s' in model %s filters.setParamsByID, already used by model %s", key, modelId, existingModel)
				}
				continue // already registered as explicit alias for this model
			}
			config.aliases[key] = modelId
			modelConfig.Aliases = append(modelConfig.Aliases, key)
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

	// Normalize routing config. The legacy top-level `matrix`/`groups` keys and
	// the new `routing.router` block are mutually exclusive: a config may use
	// either style, never both.
	hasTopLevel := config.Matrix != nil || len(config.Groups) > 0
	rtr := config.Routing.Router
	hasRouting := rtr.Use != "" || rtr.Settings.Matrix != nil || len(rtr.Settings.Groups) > 0

	if hasTopLevel && hasRouting {
		return Config{}, fmt.Errorf("config uses both the legacy top-level 'matrix'/'groups' keys and the new 'routing.router' block; please migrate the top-level keys into 'routing.router' and remove them")
	}

	if !hasTopLevel {
		// Both groups and matrix may be defined under routing.router.settings;
		// routing.router.use selects which one is active, so there is no conflict.
		rs := config.Routing.Router.Settings
		switch config.Routing.Router.Use {
		case "matrix":
			if rs.Matrix == nil {
				return Config{}, fmt.Errorf("routing.router.use is 'matrix' but routing.router.settings.matrix is not set")
			}
			config.Matrix = rs.Matrix
		case "group", "":
			config.Groups = rs.Groups
		default:
			return Config{}, fmt.Errorf("routing.router.use: unknown router %q (valid: group, matrix)", config.Routing.Router.Use)
		}
	}

	// groups XOR matrix
	if config.Matrix != nil && len(config.Groups) > 0 {
		return Config{}, fmt.Errorf("config cannot use both 'groups' and 'matrix'")
	}

	if config.Matrix != nil {
		expandedSets, err := ValidateMatrix(*config.Matrix, config.Models)
		if err != nil {
			return Config{}, fmt.Errorf("matrix: %w", err)
		}
		config.Matrix.ExpandedSets = expandedSets
	} else {
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
	}

	// Build the canonical Config.Routing from the effective result. Both legacy
	// and new-style configs converge here. The Matrix pointer is shared so
	// ExpandedSets stays in one place.
	if config.Matrix != nil {
		config.Routing.Router.Use = "matrix"
	} else {
		config.Routing.Router.Use = "group"
	}
	config.Routing.Router.Settings.Matrix = config.Matrix
	config.Routing.Router.Settings.Groups = config.Groups

	if config.Routing.Scheduler.Use == "" {
		config.Routing.Scheduler.Use = "fifo"
	}
	if config.Routing.Scheduler.Use != "fifo" {
		return Config{}, fmt.Errorf("routing.scheduler.use: unknown scheduler %q (valid: fifo)", config.Routing.Scheduler.Use)
	}
	for modelID := range config.Routing.Scheduler.Settings.Fifo.Priority {
		if _, found := config.RealModelName(modelID); !found {
			return Config{}, fmt.Errorf("routing.scheduler.settings.fifo.priority references unknown model %q", modelID)
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
			return Config{}, fmt.Errorf("apiKeys[%d]: api key cannot contain spaces", i)
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
