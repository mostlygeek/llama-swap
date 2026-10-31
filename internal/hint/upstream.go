package hint

import (
	"net/url"
	"strings"
)

// Upstream names a kind of server llama-swap sends requests to.
type Upstream string

const (
	// Unknown means the upstream was not identified; consumers fall back to a
	// best guess.
	Unknown Upstream = ""

	// Local inference servers, detected by capcompat.
	LlamaServer      Upstream = "llama-server"
	VLLM             Upstream = "vllm"
	TabbyAPI         Upstream = "tabbyapi"
	OMLX             Upstream = "omlx"
	Kyojin           Upstream = "kyojin" // owned_by "yamz" in /v1/models
	Halogen          Upstream = "halogen"
	OpenAICompatible Upstream = "openai-compatible" // any other /v1 server

	// Remote upstreams a peer can point at.
	LlamaSwap  Upstream = "llama-swap"
	OpenAI     Upstream = "openai"
	Anthropic  Upstream = "anthropic"
	OpenRouter Upstream = "openrouter"
)

// Known reports whether u is one of the constants above. Unknown itself is
// not known.
func (u Upstream) Known() bool {
	switch u {
	case LlamaServer, VLLM, TabbyAPI, OMLX, Kyojin, Halogen, OpenAICompatible,
		LlamaSwap, OpenAI, Anthropic, OpenRouter:
		return true
	}
	return false
}

// hostUpstreams maps a peer's proxy host to the provider behind it. Matching
// is on the exact host or any subdomain of it.
var hostUpstreams = []struct {
	host     string
	upstream Upstream
}{
	{"api.openai.com", OpenAI},
	{"api.anthropic.com", Anthropic},
	{"openrouter.ai", OpenRouter},
}

// ForHost guesses a remote upstream from a peer's proxy host. It returns
// Unknown when no rule matches. New rules are added to hostUpstreams.
func ForHost(host string) Upstream {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if h, _, found := strings.Cut(host, ":"); found {
		host = h
	}
	for _, rule := range hostUpstreams {
		if host == rule.host || strings.HasSuffix(host, "."+rule.host) {
			return rule.upstream
		}
	}
	return Unknown
}

// ForURL is ForHost for a parsed proxy URL. A nil URL is Unknown.
func ForURL(u *url.URL) Upstream {
	if u == nil {
		return Unknown
	}
	return ForHost(u.Hostname())
}
