package config

import (
	"fmt"
	"net/url"
	"sort"
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

// PeerModelFQN returns the fully qualified routing name for a peer model.
// Peer and model IDs may both contain slashes, so callers must treat the
// result as an exact configured name rather than parsing it by segments.
func PeerModelFQN(peerID, modelID string) string {
	return peerID + "/" + modelID
}

// ResolvePeerModel resolves an exact fully qualified peer model name or an
// unqualified model name provided by exactly one peer. Fully qualified names
// take precedence over unqualified names that happen to contain the same text.
func (c *Config) ResolvePeerModel(search string) (peerID, modelID string, found bool) {
	for id, peer := range c.Peers {
		seen := make(map[string]struct{})
		for _, model := range peer.Models {
			if _, duplicate := seen[model]; duplicate {
				continue
			}
			seen[model] = struct{}{}
			if PeerModelFQN(id, model) != search {
				continue
			}
			if found && (peerID != id || modelID != model) {
				return "", "", false
			}
			peerID, modelID, found = id, model, true
		}
	}
	if found {
		return peerID, modelID, true
	}

	for id, peer := range c.Peers {
		seen := make(map[string]struct{})
		for _, model := range peer.Models {
			if _, duplicate := seen[model]; duplicate {
				continue
			}
			seen[model] = struct{}{}
			if model != search {
				continue
			}
			if found && (peerID != id || modelID != model) {
				return "", "", false
			}
			peerID, modelID, found = id, model, true
		}
	}
	return peerID, modelID, found
}

// ValidatePeerNamespace ensures every configured peer model has an
// unambiguous, reserved fully qualified name.
func ValidatePeerNamespace(c Config) error {
	type peerTarget struct {
		peerID  string
		modelID string
	}

	fqnTargets := make(map[string]peerTarget)
	peerIDs := make([]string, 0, len(c.Peers))
	for peerID := range c.Peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	for _, peerID := range peerIDs {
		seen := make(map[string]struct{})
		for _, modelID := range c.Peers[peerID].Models {
			if _, duplicate := seen[modelID]; duplicate {
				continue
			}
			seen[modelID] = struct{}{}

			fqn := PeerModelFQN(peerID, modelID)
			if existing, ok := fqnTargets[fqn]; ok {
				return fmt.Errorf(
					"peer model name %q is ambiguous between peers.%s model %q and peers.%s model %q",
					fqn, existing.peerID, existing.modelID, peerID, modelID,
				)
			}
			fqnTargets[fqn] = peerTarget{peerID: peerID, modelID: modelID}
		}
	}

	reserved := make([]string, 0, len(fqnTargets))
	for fqn := range fqnTargets {
		reserved = append(reserved, fqn)
	}
	sort.Strings(reserved)

	for _, fqn := range reserved {
		if _, ok := c.Models[fqn]; ok {
			return fmt.Errorf("model ID %q conflicts with fully qualified peer model name", fqn)
		}
		for modelID, model := range c.Models {
			for _, alias := range model.Aliases {
				if alias == fqn {
					return fmt.Errorf("model %s alias %q conflicts with fully qualified peer model name", modelID, fqn)
				}
			}
		}
		if _, ok := c.aliases[fqn]; ok {
			return fmt.Errorf("model alias %q conflicts with fully qualified peer model name", fqn)
		}
		if _, ok := c.Selectors[fqn]; ok {
			return fmt.Errorf("selector ID %q conflicts with fully qualified peer model name", fqn)
		}
		for profileID, profile := range c.Profiles {
			if _, ok := profile.Pins[fqn]; ok {
				return fmt.Errorf("profile %s pin %q conflicts with fully qualified peer model name", profileID, fqn)
			}
		}
	}

	return nil
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
