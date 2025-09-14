package config

import (
	"fmt"
	"regexp"
)

type PeerConfig struct {
	Name        string           `yaml:"name"`
	Description string           `yaml:"description"`
	BaseURL     string           `yaml:"baseURL"`
	ApiKey      string           `yaml:"apikey"`
	Priority    int              `yaml:"priority"`
	Filters     []string         `yaml:"filters"`
	reFilters   []*regexp.Regexp `yaml:"-"`
}

// set default values for GroupConfig
func (c *PeerConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type rawConfig PeerConfig
	defaults := rawConfig{
		Name:        "",
		Description: "",
		BaseURL:     "",
		ApiKey:      "",
		Priority:    0,
		Filters:     []string{},
		reFilters:   []*regexp.Regexp{},
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	// compile regex filters and store compiled patterns in reFilters
	for _, pat := range defaults.Filters {
		r, err := regexp.Compile(pat)
		if err != nil {
			return fmt.Errorf("failed to compile peer filter %q: %w", pat, err)
		}
		defaults.reFilters = append(defaults.reFilters, r)
	}

	*c = PeerConfig(defaults)
	return nil
}
