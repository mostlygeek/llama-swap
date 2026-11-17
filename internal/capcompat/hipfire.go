package capcompat

import (
	"context"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/hint"
)

// Hipfire identifies itself in /v1/models but does not report a context
// length. Match its owner explicitly rather than weakening the default
// prober's context-length requirement for other servers.
type hipfireProber struct{}

func (hipfireProber) Name() string                          { return "hipfire" }
func (hipfireProber) Upstream(ModelsResponse) hint.Upstream { return hint.Hipfire }
func (hipfireProber) Matches(models ModelsResponse) bool {
	return strings.EqualFold(models.OwnedBy(), "hipfire")
}
func (hipfireProber) Probe(_ context.Context, _ *Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error) {
	return listingOnlyCaps(models, modelName), nil
}
