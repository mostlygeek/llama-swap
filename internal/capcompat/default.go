package capcompat

import (
	"context"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// defaultProber is the fallback prober for any OpenAI-compatible server whose
// /v1/models listing has no dedicated prober.
//
// It matches any non-empty model listing. Like the former vLLM prober, it only
// advertises what /v1/models carries directly: context length (from
// max_model_len, context_length, or meta.n_ctx) and text in/out. Features
// requiring additional endpoints or model inspection (tools, vision, audio)
// are left unset for manual configuration in models.*.capabilities.
//
// Specific engines with dedicated endpoints or capability flags (e.g.
// llama-server with /props, halogen with /health) run before this fallback.
type defaultProber struct{}

var _ Prober = defaultProber{}

func (defaultProber) Name() string { return "default" }

func (defaultProber) Matches(models ModelsResponse) bool {
	for _, entry := range models.Data {
		if entry.ContextTokens() > 0 {
			return true
		}
	}
	return false
}

func (defaultProber) Probe(_ context.Context, _ *Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error) {
	return listingOnlyCaps(models, modelName), nil
}
