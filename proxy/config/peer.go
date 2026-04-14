package config

import (
	"fmt"
	"net/url"
)

type PeerDictionaryConfig map[string]PeerConfig
type PeerConfig struct {
	Proxy    string   `yaml:"proxy"`
	ProxyURL *url.URL `yaml:"-"`
	ApiKey   string   `yaml:"apiKey"`
	Models   []string `yaml:"models"`
	Filters  Filters  `yaml:"filters"`

	// Timeout settings for proxy connections
	Timeouts TimeoutsConfig `yaml:"timeouts"`
}

func (c *PeerConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawPeerConfig PeerConfig
	defaults := rawPeerConfig{
		Proxy:   "",
		ApiKey:  "",
		Models:  []string{},
		Filters: Filters{},

		// mostly matches http.DefaultTransport but with a 60s ResponseHeader timeout
		// to match the pre PR #619 functionality
		Timeouts: TimeoutsConfig{
			Connect:        30,
			KeepAlive:      30,
			ResponseHeader: 60,
			TLSHandshake:   10,
			ExpectContinue: 1,
			IdleConn:       90,
		},
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

	// Validate models is not empty
	if len(defaults.Models) == 0 {
		return fmt.Errorf("peer models can not be empty")
	}

	*c = PeerConfig(defaults)
	return nil
}
