package proxy

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/billziss-gh/golib/shlex"
	"gopkg.in/yaml.v3"
)

const DEFAULT_GROUP_ID = "(default)"

type ModelConfig struct {
	Cmd           string   `yaml:"cmd"`
	CmdStop       string   `yaml:"cmdStop"`
	Proxy         string   `yaml:"proxy"`
	Aliases       []string `yaml:"aliases"`
	Env           []string `yaml:"env"`
	CheckEndpoint string   `yaml:"checkEndpoint"`
	UnloadAfter   int      `yaml:"ttl"`
	Unlisted      bool     `yaml:"unlisted"`
	UseModelName  string   `yaml:"useModelName"`

	// #179 for /v1/models
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Limit concurrency of HTTP requests to process
	ConcurrencyLimit int `yaml:"concurrencyLimit"`

	// Model filters see issue #174
	Filters ModelFilters `yaml:"filters"`
}

func (m *ModelConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawModelConfig ModelConfig
	defaults := rawModelConfig{
		Cmd:              "",
		CmdStop:          "",
		Proxy:            "http://localhost:${PORT}",
		Aliases:          []string{},
		Env:              []string{},
		CheckEndpoint:    "/health",
		UnloadAfter:      0,
		Unlisted:         false,
		UseModelName:     "",
		ConcurrencyLimit: 0,
		Name:             "",
		Description:      "",
	}

	// the default cmdStop to taskkill /f /t /pid ${PID}
	if runtime.GOOS == "windows" {
		defaults.CmdStop = "taskkill /f /t /pid ${PID}"
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	*m = ModelConfig(defaults)
	return nil
}

func (m *ModelConfig) SanitizedCommand() ([]string, error) {
	return SanitizeCommand(m.Cmd)
}

// ModelFilters see issue #174
type ModelFilters struct {
	StripParams string `yaml:"strip_params"`
}

func (m *ModelFilters) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawModelFilters ModelFilters
	defaults := rawModelFilters{
		StripParams: "",
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	*m = ModelFilters(defaults)
	return nil
}

func (f ModelFilters) SanitizedStripParams() ([]string, error) {
	if f.StripParams == "" {
		return nil, nil
	}

	params := strings.Split(f.StripParams, ",")
	cleaned := make([]string, 0, len(params))

	for _, param := range params {
		trimmed := strings.TrimSpace(param)
		if trimmed == "model" || trimmed == "" {
			continue
		}
		cleaned = append(cleaned, trimmed)
	}

	// sort cleaned
	slices.Sort(cleaned)
	return cleaned, nil
}

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
	Macros map[string]string `yaml:"macros"`

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
	- name can not be any reserved macros: PORT
	- macro values must be less than 1024 characters
	*/
	macroNameRegex := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	for macroName, macroValue := range config.Macros {
		if len(macroName) >= 64 {
			return Config{}, fmt.Errorf("macro name '%s' exceeds maximum length of 63 characters", macroName)
		}
		if !macroNameRegex.MatchString(macroName) {
			return Config{}, fmt.Errorf("macro name '%s' contains invalid characters, must match pattern ^[a-zA-Z0-9_-]+$", macroName)
		}
		if len(macroValue) >= 1024 {
			return Config{}, fmt.Errorf("macro value for '%s' exceeds maximum length of 1024 characters", macroName)
		}
		switch macroName {
		case "PORT":
			return Config{}, fmt.Errorf("macro name '%s' is reserved and cannot be used", macroName)
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

		// go through model config fields: cmd, cmdStop, proxy, checkEndPoint and replace macros with macro values
		for macroName, macroValue := range config.Macros {
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
