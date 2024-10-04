package proxy

import (
	"os"

	"gopkg.in/yaml.v3"
)

type ModelConfig struct {
	Cmd     string   `yaml:"cmd"`
	Proxy   string   `yaml:"proxy"`
	Aliases []string `yaml:"aliases"`
}

type Config struct {
	Models             map[string]ModelConfig `yaml:"models"`
	HealthCheckTimeout int                    `yaml:"healthCheckTimeout"`
}

func (c *Config) FindConfig(modelName string) (ModelConfig, bool) {
	modelConfig, found := c.Models[modelName]
	if found {
		return modelConfig, true
	}

	// Search through aliases to find the right config
	for _, config := range c.Models {
		for _, alias := range config.Aliases {
			if alias == modelName {
				return config, true
			}
		}
	}

	return ModelConfig{}, false
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
