package capcompat

import (
	"context"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// halogenOwner is what halogen-flash-server reports in /v1/models.
const halogenOwner = "halogen"

// halogenHealth is the subset of halogen-flash-server's GET /health that
// describes what the running build accepts. Its own documentation calls that
// endpoint the authoritative account of the build's capabilities, and it is
// the only place several of them appear: the model listing carries context
// and nothing else.
type halogenHealth struct {
	// Context is the loaded context window. slot_ctx reports the same number
	// per slot on the deployments seen so far.
	Context int `json:"context"`

	// Vision is absent or disabled unless the server was started with a
	// vision tower, so image input genuinely varies per deployment and has
	// to be read rather than assumed for the engine.
	Vision struct {
		Enabled bool `json:"enabled"`
	} `json:"vision"`

	// ToolCalls describes the tool-call wire format. A build without tool
	// support has nothing to describe.
	ToolCalls struct {
		WireFormat string `json:"wire_format"`
	} `json:"tool_calls"`

	// Supported lists the request fields the build accepts, "tools" among
	// them. This is the server stating what it takes, so it is the primary
	// signal.
	Supported []string `json:"supported"`
}

// halogenProber reads capabilities from halogen-flash-server, an
// OpenAI-compatible engine for Qwen3.8-Flash-Next on AMD Strix Halo.
//
// It reads /v1/models for the model entry and /health for everything else.
// The listing publishes the context length under three names at once
// (max_model_len, context_length and meta.n_ctx), all agreeing, but says
// nothing about images or tools.
//
// Reranking is not reported anywhere and stays a manual setting.
type halogenProber struct{}

var _ Prober = halogenProber{}

func (halogenProber) Name() string { return "halogen" }

func (halogenProber) Matches(models ModelsResponse) bool {
	return strings.EqualFold(models.OwnedBy(), halogenOwner)
}

func (p halogenProber) Probe(ctx context.Context, c *Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error) {
	var health halogenHealth
	if err := c.GetJSON(ctx, "/health", &health); err != nil {
		return config.ModelCapConfig{}, err
	}

	caps := config.ModelCapConfig{
		In:  []string{"text"},
		Out: []string{"text"},
	}
	if health.Vision.Enabled {
		caps.In = append(caps.In, "image")
	}
	caps.Tools = health.supportsTools()

	// /health is the authoritative report, so its context wins. The listing
	// is the fallback for a build that omits it.
	caps.Context = health.Context
	if caps.Context == 0 {
		// max_tokens_cap and max_tokens_default bound one request's output
		// rather than the window, so they are deliberately not read here.
		caps.Context = listingOnlyCaps(models, modelName).Context
	}
	return caps, nil
}

// supportsTools reports whether the build accepts tool calls. The supported
// list is the server naming the request fields it takes; the wire format is
// a second signal for a build that does not publish that list.
func (h halogenHealth) supportsTools() bool {
	for _, field := range h.Supported {
		if strings.EqualFold(field, "tools") {
			return true
		}
	}
	return strings.TrimSpace(h.ToolCalls.WireFormat) != ""
}
