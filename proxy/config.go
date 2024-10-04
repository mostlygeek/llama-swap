package proxy

import (
	"os"

	"gopkg.in/yaml.v3"
)

type ModelConfig struct {
	Cmd   string `yaml:"cmd"`
	Proxy string `yaml:"proxy"`
}

type Config struct {
	Models map[string]ModelConfig `yaml:"models"`
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

	return &config, nil
}
