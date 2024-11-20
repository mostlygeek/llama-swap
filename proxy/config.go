package proxy

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type ModelConfig struct {
	Cmd           string   `yaml:"cmd"`
	Proxy         string   `yaml:"proxy"`
	Aliases       []string `yaml:"aliases"`
	Env           []string `yaml:"env"`
	CheckEndpoint string   `yaml:"checkEndpoint"`
	UnloadAfter   int      `yaml:"ttl"`
}

func (m *ModelConfig) SanitizedCommand() ([]string, error) {
	return SanitizeCommand(m.Cmd)
}

type Config struct {
	Models             map[string]ModelConfig `yaml:"models"`
	HealthCheckTimeout int                    `yaml:"healthCheckTimeout"`
}

func (c *Config) FindConfig(modelName string) (ModelConfig, string, bool) {
	modelConfig, found := c.Models[modelName]
	if found {
		return modelConfig, modelName, true
	}

	// Search through aliases to find the right config
	for actual, config := range c.Models {
		for _, alias := range config.Aliases {
			if alias == modelName {
				return config, actual, true
			}
		}
	}

	return ModelConfig{}, "", false
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

	return &config, nil
}

func SanitizeCommand(cmdStr string) ([]string, error) {
	// Remove trailing backslashes
	cmdStr = strings.ReplaceAll(cmdStr, "\\ \n", " ")
	cmdStr = strings.ReplaceAll(cmdStr, "\\\n", " ")

	// Split the command into arguments
	args := strings.Fields(cmdStr)

	// Ensure the command is not empty
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	return args, nil
}
