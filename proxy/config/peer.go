package config

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
)

// ProtectedPeerParams is a list of parameters that cannot be set via filters.setParams
// These are protected to prevent breaking the proxy's ability to route requests correctly
var ProtectedPeerParams = []string{"model"}

type PeerDictionaryConfig map[string]PeerConfig
type PeerConfig struct {
	Proxy    string   `yaml:"proxy"`
	ProxyURL *url.URL `yaml:"-"`
	ApiKey   string   `yaml:"apiKey"`
	Models   []string `yaml:"models"`
	Filters  PeerFilters `yaml:"filters"`
}

// PeerFilters contains filter settings for peer requests
type PeerFilters struct {
	// SetParams is a dictionary of parameters to set/override in requests to this peer
	// Protected params (like "model") cannot be set
	SetParams map[string]any `yaml:"setParams"`
}

// SanitizedSetParams returns a copy of SetParams with protected params removed
// and keys sorted for consistent iteration order
func (f PeerFilters) SanitizedSetParams() (map[string]any, []string) {
	if len(f.SetParams) == 0 {
		return nil, nil
	}

	result := make(map[string]any, len(f.SetParams))
	keys := make([]string, 0, len(f.SetParams))

	for key, value := range f.SetParams {
		// Skip protected params
		if slices.Contains(ProtectedPeerParams, key) {
			continue
		}
		result[key] = value
		keys = append(keys, key)
	}

	// Sort keys for consistent ordering
	sort.Strings(keys)

	if len(result) == 0 {
		return nil, nil
	}

	return result, keys
}

func (c *PeerConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawPeerConfig PeerConfig
	defaults := rawPeerConfig{
		Proxy:   "",
		ApiKey:  "",
		Models:  []string{},
		Filters: PeerFilters{},
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
