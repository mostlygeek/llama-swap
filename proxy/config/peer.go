package config

import (
	"fmt"
	"net/url"
	"strings"
)

type PeerDictionaryConfig map[string]PeerConfig
type PeerConfig struct {
	Proxy       string   `yaml:"proxy"`
	ProxyURL    *url.URL `yaml:"-"`
	ApiKey      string   `yaml:"apiKey"`
	Models      []string `yaml:"models"`
	Filters     Filters  `yaml:"filters"`
	PathRewrite string   `yaml:"pathRewrite"`
}

func (c *PeerConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawPeerConfig PeerConfig
	defaults := rawPeerConfig{
		Proxy:       "",
		ApiKey:      "",
		Models:      []string{},
		Filters:     Filters{},
		PathRewrite: "",
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	// Validate proxy is not empty
	if defaults.Proxy == "" {
		return fmt.Errorf("proxy is required")
	}

	// Validate proxy is a valid URL and store the parsed value
	parsedURL, err := url.Parse(defaults.Proxy)
	if err != nil {
		return fmt.Errorf("invalid peer proxy URL (%s): %w", defaults.Proxy, err)
	}
	defaults.ProxyURL = parsedURL

	// Validate pathRewrite format if specified
	if defaults.PathRewrite != "" {
		if !strings.HasPrefix(defaults.PathRewrite, "strip:") && !strings.HasPrefix(defaults.PathRewrite, "replace:") {
			return fmt.Errorf("invalid pathRewrite format: must start with 'strip:' or 'replace:'")
		}
	}

	// Validate models is not empty
	if len(defaults.Models) == 0 {
		return fmt.Errorf("peer models can not be empty")
	}

	*c = PeerConfig(defaults)
	return nil
}
