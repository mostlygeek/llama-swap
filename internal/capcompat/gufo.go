package capcompat

import (
	"context"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// gufoOwner is what Gufo reports in /v1/models.
const gufoOwner = "gufo"

// gufoProber reads capabilities from Gufo's /v1/models listing.
//
// Like vLLM, Gufo's listing carries context_length, but nothing it serves
// reports whether the deployment loaded an mmproj vision tower or enabled
// specific tool support. So only context length and text in/out are advertised
// automatically, matching listingOnlyCaps; tools and non-text modalities remain
// manual settings in models.*.capabilities.
type gufoProber struct{}

var _ Prober = gufoProber{}

func (gufoProber) Name() string { return "gufo" }

func (gufoProber) Matches(models ModelsResponse) bool {
	return strings.EqualFold(models.OwnedBy(), gufoOwner)
}

func (gufoProber) Probe(_ context.Context, _ *Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error) {
	return listingOnlyCaps(models, modelName), nil
}
