package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/tailcat"
)

const TailcatEphemeralKey = "ephemeral"

// TailcatConfig configures the private Tailcat listener and its HTTP policy.
// The listener identity comes from the startup-only -listen-tailcat flag.
type TailcatConfig struct {
	Allow  []string `yaml:"allow"`
	Models []string `yaml:"models"`
	Admin  bool     `yaml:"admin"`
	Debug  bool     `yaml:"debug"`

	AllowedClients []string `yaml:"-"`
}

func validatePeerTailcat(peer *rawPeerConfig) error {
	u, err := url.Parse(peer.Proxy)
	if err != nil {
		return fmt.Errorf("invalid peer proxy URL (%s): %w", peer.Proxy, err)
	}
	isTailcat := strings.EqualFold(u.Scheme, "tailcat")
	if !isTailcat {
		if peer.TailcatKey != "" {
			return fmt.Errorf("tailcatKey is only valid with a tailcat:// proxy")
		}
		return nil
	}

	if !strings.EqualFold(u.Scheme, "tailcat") || u.Opaque != "" || u.User != nil || u.Host == "" ||
		u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("invalid Tailcat peer proxy URL %q: expected tailcat://<connection-token> with no credentials, path, query, fragment, or port", peer.Proxy)
	}
	// Connection blobs use base64url and therefore never contain a colon. This
	// catches explicit ports without passing the case-sensitive token through
	// URL hostname normalization.
	if strings.Contains(u.Host, ":") {
		return fmt.Errorf("invalid Tailcat peer proxy URL %q: explicit ports are not allowed (Tailcat uses port 80)", peer.Proxy)
	}
	blob := u.Host
	if err := tailcat.ValidateConnectionBlob(blob); err != nil {
		return fmt.Errorf("invalid Tailcat connection token: %w", err)
	}
	peer.tailcatBlob = blob
	peer.ProxyURL = &url.URL{Scheme: "http", Host: "server.tailcat"}

	if peer.TailcatKey != "" && peer.TailcatKey != TailcatEphemeralKey {
		loaded, err := tailcat.LoadPrivateKey(peer.TailcatKey, false)
		if err != nil {
			return fmt.Errorf("tailcatKey: %w", err)
		}
		peer.tailcatPrivateKey = loaded
	}
	return nil
}

func validateTailcatConfig(cfg *Config) error {
	if tc := cfg.Tailcat; tc != nil {
		seenAllow := make(map[string]struct{}, len(tc.Allow))
		allowedClients := make([]string, 0, len(tc.Allow))
		for i, raw := range tc.Allow {
			canonical, err := tailcat.ValidateNodePublic(raw)
			if err != nil {
				return fmt.Errorf("tailcat.allow[%d]: invalid node public key: %w", i, err)
			}
			if canonical != raw {
				return fmt.Errorf("tailcat.allow[%d]: must be a full canonical nodekey: public key", i)
			}
			if _, exists := seenAllow[canonical]; exists {
				continue
			}
			seenAllow[canonical] = struct{}{}
			allowedClients = append(allowedClients, canonical)
		}
		tc.Allow = allowedClients
		tc.AllowedClients = allowedClients

		validModels := publicTailcatModelIDs(*cfg)
		seenModels := make(map[string]struct{}, len(tc.Models))
		models := make([]string, 0, len(tc.Models))
		for i, model := range tc.Models {
			if model == "" {
				return fmt.Errorf("tailcat.models[%d]: model ID cannot be empty", i)
			}
			if model != "*" {
				if _, ok := validModels[model]; !ok {
					return fmt.Errorf("tailcat.models[%d]: unknown public model ID %q", i, model)
				}
			}
			if _, exists := seenModels[model]; exists {
				continue
			}
			seenModels[model] = struct{}{}
			models = append(models, model)
		}
		tc.Models = models
	}

	// One stable client identity may safely share transports to the same
	// destination, but using it for different Tailcat servers creates an
	// ambiguous network identity and is rejected up front.
	type destination struct{ server string }
	used := map[string]destination{}
	for peerID, peer := range cfg.Peers {
		clientKey, blob, pk, found := peer.Tailcat()
		if !found || clientKey == "" || clientKey == TailcatEphemeralKey {
			continue
		}
		client := pk.Identity()
		server, _ := tailcat.ConnectionDestination(blob)
		if prev, ok := used[client]; ok && prev.server != server {
			return fmt.Errorf("peers.%s.tailcatKey reuses client node key %s for a different Tailcat destination", peerID, client)
		}
		used[client] = destination{server: server}
	}
	return nil
}

func publicTailcatModelIDs(cfg Config) map[string]struct{} {
	ids := make(map[string]struct{})
	for id, model := range cfg.Models {
		ids[id] = struct{}{}
		for _, alias := range model.Aliases {
			ids[alias] = struct{}{}
		}
	}
	for id := range cfg.Selectors {
		ids[id] = struct{}{}
	}
	for _, profile := range cfg.Profiles {
		for pin := range profile.Pins {
			ids[pin] = struct{}{}
		}
	}
	for peerID, peer := range cfg.Peers {
		for _, model := range peer.Models {
			ids[PeerModelFQN(peerID, model)] = struct{}{}
		}
	}
	return ids
}
