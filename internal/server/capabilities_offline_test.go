package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mostlygeek/llama-swap/internal/config"
)

func TestAPI_ListModelsEstimatesContextFromCmd(t *testing.T) {
	// A model that has never been probed still gets its serving window into
	// the listing, because the config's own -c states it.
	mc := config.ModelConfig{Cmd: "llama-server -m a.gguf -c 262144", Proxy: "http://localhost:9001"}
	rec := listModel(t, capServer(t, "m", mc))

	assert.Equal(t, []any{"text"}, rec.Architecture["input_modalities"])
	assert.Equal(t, []any{"text"}, rec.Architecture["output_modalities"])
	assert.Nil(t, rec.Capabilities)
	assert.Equal(t, 262144, rec.ContextLength)
	assert.Equal(t, 262144, rec.ContextWindow)
}

func TestAPI_ListModelsDisableAutoSuppressesOfflineEstimate(t *testing.T) {
	mc := config.ModelConfig{
		Cmd:          "llama-server -m a.gguf -c 262144",
		Proxy:        "http://localhost:9001",
		Capabilities: config.ModelCapConfig{DisableAuto: true},
	}
	rec := listModel(t, capServer(t, "m", mc))

	assert.Zero(t, rec.ContextLength)
}
