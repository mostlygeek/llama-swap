package capcompat

import (
	"context"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// vllmOwner is what vLLM reports in /v1/models.
const vllmOwner = "vllm"

// vllmProber reads capabilities from vLLM's /v1/models listing.
//
// vLLM is deliberately thin here. Its listing carries max_model_len, which is
// the most requested field, but nothing it serves reveals whether the server
// was started with --enable-auto-tool-choice or whether the model accepts
// images: /version, /health and /server_info do not report either. So tools
// and non-text modalities stay manual settings for vLLM models, and the guide
// says so.
type vllmProber struct{}

var _ Prober = vllmProber{}

func (vllmProber) Name() string { return "vllm" }

func (vllmProber) Matches(models ModelsResponse) bool {
	return strings.EqualFold(models.OwnedBy(), vllmOwner)
}

func (vllmProber) Probe(_ context.Context, _ *Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error) {
	return listingOnlyCaps(models, modelName), nil
}
