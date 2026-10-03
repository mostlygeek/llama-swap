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
// Gufo (AMD Strix Halo optimized inference engine) reports context_length
// directly in its OpenAI-compatible /v1/models response. It also has built-in
// tool call support and vision (mmproj) support for compatible models.
type gufoProber struct{}

var _ Prober = gufoProber{}

func (gufoProber) Name() string { return "gufo" }

func (gufoProber) Matches(models ModelsResponse) bool {
	return strings.EqualFold(models.OwnedBy(), gufoOwner)
}

func (gufoProber) Probe(_ context.Context, _ *Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error) {
	caps := listingOnlyCaps(models, modelName)
	caps.Tools = true
	caps.In = []string{"text", "image"}
	return caps, nil
}
