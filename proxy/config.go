package proxy

import (
	"fmt"
	"os"
	"strings"

	"github.com/google/shlex"
	"gopkg.in/yaml.v3"
)

type ModelConfig struct {
	Cmd           string   `yaml:"cmd"`
	Proxy         string   `yaml:"proxy"`
	Aliases       []string `yaml:"aliases"`
	Env           []string `yaml:"env"`
	CheckEndpoint string   `yaml:"checkEndpoint"`
	UnloadAfter   int      `yaml:"ttl"`
	Unlisted      bool     `yaml:"unlisted"`
}

func (m *ModelConfig) SanitizedCommand() ([]string, error) {
	return SanitizeCommand(m.Cmd)
}

type Config struct {
	HealthCheckTimeout int                    `yaml:"healthCheckTimeout"`
	LogRequests        bool                   `yaml:"logRequests"`
	Models             map[string]ModelConfig `yaml:"models"`
	Profiles           map[string][]string    `yaml:"profiles"`

	// map aliases to actual model IDs
	aliases map[string]string
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

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	if config.HealthCheckTimeout < 15 {
		config.HealthCheckTimeout = 15
	}

	// Populate the aliases map
	config.aliases = make(map[string]string)
	for modelName, modelConfig := range config.Models {
		for _, alias := range modelConfig.Aliases {
			config.aliases[alias] = modelName
		}
	}

	return &config, nil
}

func SanitizeCommand(cmdStr string) ([]string, error) {
	// Remove trailing backslashes
	cmdStr = strings.ReplaceAll(cmdStr, "\\ \n", " ")
	cmdStr = strings.ReplaceAll(cmdStr, "\\\n", " ")

	// Split the command into arguments
	args, err := shlex.Split(cmdStr)
	if err != nil {
		return nil, err
	}

	// Ensure the command is not empty
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	return args, nil
}
