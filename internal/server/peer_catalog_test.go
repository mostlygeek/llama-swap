package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_PeerCatalogModelsInListings(t *testing.T) {
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
peers:
  kitchen:
    proxy: http://kitchen.invalid
    models:
      - static
      - add .*
`))
	require.NoError(t, err)
	cfg.SetDiscoveredPeerModels("kitchen", []config.DiscoveredPeerModel{
		{ID: "qwen", Record: map[string]any{
			"id":             "qwen",
			"name":           "Qwen 3",
			"description":    "a model",
			"context_length": float64(32768),
			"owned_by":       "kitchen-owner",
			"status":         map[string]any{"value": "loaded"},
			"meta": map[string]any{
				"llamaswap": map[string]any{"type": "model", "modelID": "qwen", "family": "qwen"},
			},
		}},
	})

	s := newTestServerWithConfig(cfg, newStubRouter(nil, ""), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data []modelRecord `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	byID := make(map[string]modelRecord)
	for _, rec := range resp.Data {
		byID[rec.ID] = rec
	}

	require.Contains(t, byID, "kitchen/static")
	assert.Equal(t, "kitchen: static", byID["kitchen/static"].Name)

	rec, ok := byID["kitchen/qwen"]
	require.True(t, ok, "discovered model listed: %v", byID)
	assert.Equal(t, "kitchen: Qwen 3", rec.Name)
	assert.Equal(t, "a model", rec.Description)
	assert.Equal(t, 32768, rec.ContextLength)
	assert.Equal(t, 32768, rec.ContextWindow)
	assert.Equal(t, "kitchen-owner", rec.OwnedBy)
	assert.Equal(t, "loaded", rec.Status["value"])
	assert.Equal(t, map[string]any{"type": "peer", "peerID": "kitchen", "family": "qwen"}, rec.Meta["llamaswap"])

	var status *apiModel
	for _, m := range s.modelStatus() {
		if m.Id == "kitchen/qwen" {
			status = &m
		}
	}
	require.NotNil(t, status)
	assert.Equal(t, "kitchen", status.PeerID)
	assert.Equal(t, "Qwen 3", status.Name)
	assert.Equal(t, 32768, status.ContextLength)

	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/peers", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `[{"id":"kitchen","models":2}]`, w.Body.String())
}

func TestServer_APIRefreshPeer(t *testing.T) {
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
peers:
  kitchen:
    proxy: http://kitchen.invalid
    models:
      - static
`))
	require.NoError(t, err)
	s := newTestServerWithConfig(cfg, newStubRouter(nil, ""), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/peers/kitchen/refresh", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"id":"kitchen","models":1}`, w.Body.String())

	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/peers/nope/refresh", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
