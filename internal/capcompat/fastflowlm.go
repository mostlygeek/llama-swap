package capcompat

import (
	"context"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/hint"
)

// FastFlowLM reports its owner even in versions that omit the context length.
type fastFlowLMProber struct{}

func (fastFlowLMProber) Name() string                          { return "fastflowlm" }
func (fastFlowLMProber) Upstream(ModelsResponse) hint.Upstream { return hint.FastFlowLM }
func (fastFlowLMProber) Matches(models ModelsResponse) bool {
	return strings.EqualFold(models.OwnedBy(), "FastFlowLM")
}
func (fastFlowLMProber) Probe(_ context.Context, _ *Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error) {
	return listingOnlyCaps(models, modelName), nil
}
