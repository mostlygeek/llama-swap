package config

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/billziss-gh/golib/shlex"
	"gopkg.in/yaml.v3"
)

const DEFAULT_GROUP_ID = "(default)"

type MacroList map[string]string

type GroupConfig struct {
	Swap       bool     `yaml:"swap"`
	Exclusive  bool     `yaml:"exclusive"`
	Persistent bool     `yaml:"persistent"`
	Members    []string `yaml:"members"`
}

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
	MetricsMaxInMemory int                    `yaml:"metricsMaxInMemory"`
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

	// default configuration values
	config := Config{
		HealthCheckTimeout: 120,
		StartPort:          5800,
		LogLevel:           "info",
		MetricsMaxInMemory: 1000,
	}
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return Config{}, err
	}

	if config.HealthCheckTimeout < 15 {
		// set a minimum of 15 seconds
		config.HealthCheckTimeout = 15
	}

	if config.StartPort < 1 {
		return Config{}, fmt.Errorf("startPort must be greater than 1")
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

	/* check macro constraint rules:

	- name must fit the regex ^[a-zA-Z0-9_-]+$
	- names must be less than 64 characters (no reason, just cause)
	- name can not be any reserved macros: PORT, MODEL_ID
	- macro values must be less than 1024 characters
	*/
	for macroName, macroValue := range config.Macros {
		if err = validateMacro(macroName, macroValue); err != nil {
			return Config{}, err
		}
	}

	// Get and sort all model IDs first, makes testing more consistent
	modelIds := make([]string, 0, len(config.Models))
	for modelId := range config.Models {
		modelIds = append(modelIds, modelId)
	}
	sort.Strings(modelIds) // This guarantees stable iteration order

	nextPort := config.StartPort
	for _, modelId := range modelIds {
		modelConfig := config.Models[modelId]

		// Strip comments from command fields before macro expansion
		modelConfig.Cmd = StripComments(modelConfig.Cmd)
		modelConfig.CmdStop = StripComments(modelConfig.CmdStop)

		// validate model macros
		for macroName, macroValue := range modelConfig.Macros {
			if err = validateMacro(macroName, macroValue); err != nil {
				return Config{}, fmt.Errorf("model %s: %s", modelId, err.Error())
			}
		}

		// Merge global config and model macros. Model macros take precedence
		mergedMacros := make(MacroList)
		for k, v := range config.Macros {
			mergedMacros[k] = v
		}
		for k, v := range modelConfig.Macros {
			mergedMacros[k] = v
		}

		// go through model config fields: cmd, cmdStop, proxy, checkEndPoint and replace macros with macro values
		for macroName, macroValue := range mergedMacros {
			macroSlug := fmt.Sprintf("${%s}", macroName)
			modelConfig.Cmd = strings.ReplaceAll(modelConfig.Cmd, macroSlug, macroValue)
			modelConfig.CmdStop = strings.ReplaceAll(modelConfig.CmdStop, macroSlug, macroValue)
			modelConfig.Proxy = strings.ReplaceAll(modelConfig.Proxy, macroSlug, macroValue)
			modelConfig.CheckEndpoint = strings.ReplaceAll(modelConfig.CheckEndpoint, macroSlug, macroValue)
			modelConfig.Filters.StripParams = strings.ReplaceAll(modelConfig.Filters.StripParams, macroSlug, macroValue)
		}

		// enforce ${PORT} used in both cmd and proxy
		if !strings.Contains(modelConfig.Cmd, "${PORT}") && strings.Contains(modelConfig.Proxy, "${PORT}") {
			return Config{}, fmt.Errorf("model %s: proxy uses ${PORT} but cmd does not - ${PORT} is only available when used in cmd", modelId)
		}

		// only iterate over models that use ${PORT} to keep port numbers from increasing unnecessarily
		if strings.Contains(modelConfig.Cmd, "${PORT}") || strings.Contains(modelConfig.Proxy, "${PORT}") || strings.Contains(modelConfig.CmdStop, "${PORT}") {
			nextPortStr := strconv.Itoa(nextPort)
			modelConfig.Cmd = strings.ReplaceAll(modelConfig.Cmd, "${PORT}", nextPortStr)
			modelConfig.CmdStop = strings.ReplaceAll(modelConfig.CmdStop, "${PORT}", nextPortStr)
			modelConfig.Proxy = strings.ReplaceAll(modelConfig.Proxy, "${PORT}", nextPortStr)
			nextPort++
		}

		if strings.Contains(modelConfig.Cmd, "${MODEL_ID}") || strings.Contains(modelConfig.CmdStop, "${MODEL_ID}") {
			modelConfig.Cmd = strings.ReplaceAll(modelConfig.Cmd, "${MODEL_ID}", modelId)
			modelConfig.CmdStop = strings.ReplaceAll(modelConfig.CmdStop, "${MODEL_ID}", modelId)
		}

		// make sure there are no unknown macros that have not been replaced
		macroPattern := regexp.MustCompile(`\$\{([a-zA-Z0-9_-]+)\}`)
		fieldMap := map[string]string{
			"cmd":           modelConfig.Cmd,
			"cmdStop":       modelConfig.CmdStop,
			"proxy":         modelConfig.Proxy,
			"checkEndpoint": modelConfig.CheckEndpoint,
		}

		for fieldName, fieldValue := range fieldMap {
			matches := macroPattern.FindAllStringSubmatch(fieldValue, -1)
			for _, match := range matches {
				macroName := match[1]
				if macroName == "PID" && fieldName == "cmdStop" {
					continue // this is ok, has to be replaced by process later
				}
				if _, exists := config.Macros[macroName]; !exists {
					return Config{}, fmt.Errorf("unknown macro '${%s}' found in %s.%s", macroName, modelId, fieldName)
				}
			}
		}

		config.Models[modelId] = modelConfig
	}

	config = AddDefaultGroupToConfig(config)
	// check that members are all unique in the groups
	memberUsage := make(map[string]string) // maps member to group it appears in
	for groupID, groupConfig := range config.Groups {
		prevSet := make(map[string]bool)
		for _, member := range groupConfig.Members {
			// Check for duplicates within this group
			if _, found := prevSet[member]; found {
				return Config{}, fmt.Errorf("duplicate model member %s found in group: %s", member, groupID)
			}
			prevSet[member] = true

			// Check if member is used in another group
			if existingGroup, exists := memberUsage[member]; exists {
				return Config{}, fmt.Errorf("model member %s is used in multiple groups: %s and %s", member, existingGroup, groupID)
			}
			memberUsage[member] = groupID
		}
	}

	// clean up hooks preload
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
		for modelName, _ := range config.Models {
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

var (
	macroNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// validateMacro validates macro name and value constraints
func validateMacro(name, value string) error {
	if len(name) >= 64 {
		return fmt.Errorf("macro name '%s' exceeds maximum length of 63 characters", name)
	}
	if !macroNameRegex.MatchString(name) {
		return fmt.Errorf("macro name '%s' contains invalid characters, must match pattern ^[a-zA-Z0-9_-]+$", name)
	}
	if len(value) >= 1024 {
		return fmt.Errorf("macro value for '%s' exceeds maximum length of 1024 characters", name)
	}

	switch name {
	case "PORT":
	case "MODEL_ID":
		return fmt.Errorf("macro name '%s' is reserved and cannot be used", name)
	}

	return nil
}
