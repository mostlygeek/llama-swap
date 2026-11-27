package capcompat

import (
	"context"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/hint"
)

// tensorFold reports owned_by "tensorfold" in /v1/models. Its listing carries
// the context window under both context_length and max_model_len, plus a
// capabilities object this package does not read; text in and out is all the
// listing states in a shape the other probers understand.
//
// It matches on the owner alone, because a build that leaves the window
// unspecified omits both context fields and defaultProber would report
// ErrUnsupportedUpstream.
type tensorFoldProber struct{}

var _ Prober = tensorFoldProber{}

func (tensorFoldProber) Name() string { return "tensorfold" }

func (tensorFoldProber) Upstream(ModelsResponse) hint.Upstream { return hint.TensorFold }

func (tensorFoldProber) Matches(models ModelsResponse) bool {
	return strings.EqualFold(models.OwnedBy(), "tensorfold")
}

func (p tensorFoldProber) Probe(_ context.Context, _ *Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error) {
	return listingOnlyCaps(models, modelName), nil
}
