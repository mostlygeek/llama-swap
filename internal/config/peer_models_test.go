package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPeerConfig_CatalogDefaults(t *testing.T) {
	t.Run("literal models do not enable discovery", func(t *testing.T) {
		var peer PeerConfig
		require.NoError(t, yaml.Unmarshal([]byte("proxy: http://h\nmodels: [a, b]\n"), &peer))
		assert.False(t, peer.Discovery())
		assert.Equal(t, []string{"a", "b"}, peer.LiteralModels())
	})

	t.Run("omitted models discover everything", func(t *testing.T) {
		var peer PeerConfig
		require.NoError(t, yaml.Unmarshal([]byte("proxy: http://h\n"), &peer))
		require.True(t, peer.Discovery())
		assert.Equal(t, &PeerCatalogConfig{Path: "/v1/models", Refresh: 120, Limit: 512}, peer.Catalog)
		selected, _ := peer.SelectCatalogModels([]string{"x", "y"})
		assert.Equal(t, []string{"x", "y"}, selected)
	})

	t.Run("add rule enables discovery", func(t *testing.T) {
		var peer PeerConfig
		require.NoError(t, yaml.Unmarshal([]byte("proxy: http://h\nmodels:\n  - a\n  - add qwen\n"), &peer))
		assert.True(t, peer.Discovery())
		assert.Equal(t, []string{"a"}, peer.LiteralModels())
	})

	t.Run("catalog settings are kept", func(t *testing.T) {
		var peer PeerConfig
		require.NoError(t, yaml.Unmarshal([]byte(`
proxy: http://h
catalog:
  path: /api/models
  refresh: 30
  limit: 2
  transitive: true
models:
  - a
`), &peer))
		assert.Equal(t, &PeerCatalogConfig{Path: "/api/models", Refresh: 30, Limit: 2, Transitive: true}, peer.Catalog)
	})

	t.Run("keyword must be followed by whitespace", func(t *testing.T) {
		var peer PeerConfig
		require.NoError(t, yaml.Unmarshal([]byte("proxy: http://h\nmodels:\n  - address-model\n  - removed-me\n  - \"add\\tqwen\"\n"), &peer))
		assert.Equal(t, []string{"address-model", "removed-me"}, peer.LiteralModels())
		assert.True(t, peer.Discovery())
		selected, _ := peer.SelectCatalogModels([]string{"qwen-8b", "llama"})
		assert.Equal(t, []string{"qwen-8b"}, selected)
	})

	t.Run("regex may contain colons and spaces", func(t *testing.T) {
		var peer PeerConfig
		require.NoError(t, yaml.Unmarshal([]byte("proxy: http://h\nmodels:\n  - add  qwen:(8b|32b) q4\n"), &peer))
		selected, _ := peer.SelectCatalogModels([]string{"qwen:8b q4", "qwen:14b q4"})
		assert.Equal(t, []string{"qwen:8b q4"}, selected)
	})
}

func TestPeerConfig_SelectCatalogModels(t *testing.T) {
	load := func(t *testing.T, rules string) PeerConfig {
		t.Helper()
		var peer PeerConfig
		require.NoError(t, yaml.Unmarshal([]byte("proxy: http://h\nmodels:\n"+rules), &peer))
		return peer
	}
	catalog := []string{"qwen-8b", "qwen-32b", "qwen-embed", "llama-8b", "literal"}

	tests := []struct {
		name  string
		rules string
		want  []string
	}{
		{
			name:  "add matches anywhere in the id",
			rules: "  - add 8b\n",
			want:  []string{"llama-8b", "qwen-8b"},
		},
		{
			name:  "remove drops earlier adds",
			rules: "  - add qwen\n  - remove embed\n",
			want:  []string{"qwen-32b", "qwen-8b"},
		},
		{
			name:  "later add brings models back",
			rules: "  - add qwen\n  - remove qwen-8b\n  - add 8b\n",
			want:  []string{"llama-8b", "qwen-32b", "qwen-8b", "qwen-embed"},
		},
		{
			name:  "remove does not affect later adds",
			rules: "  - add qwen\n  - remove llama\n  - add llama\n",
			want:  []string{"llama-8b", "qwen-32b", "qwen-8b", "qwen-embed"},
		},
		{
			name:  "literals are not returned and not removed",
			rules: "  - literal\n  - add .*\n  - remove literal|qwen\n",
			want:  []string{"llama-8b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peer := load(t, tt.rules)
			got, truncated := peer.SelectCatalogModels(catalog)
			assert.Equal(t, tt.want, got)
			assert.Zero(t, truncated)
			if strings.Contains(tt.rules, "- literal") {
				assert.Equal(t, []string{"literal"}, peer.LiteralModels())
			}
		})
	}

	t.Run("limit keeps the first ids in sorted order", func(t *testing.T) {
		var peer PeerConfig
		require.NoError(t, yaml.Unmarshal([]byte("proxy: http://h\ncatalog:\n  limit: 2\n"), &peer))
		got, truncated := peer.SelectCatalogModels(catalog)
		assert.Equal(t, []string{"literal", "llama-8b"}, got)
		assert.Equal(t, 3, truncated)
	})
}

func TestConfig_SetDiscoveredPeerModels(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(`
models:
  local:
    cmd: llama-server --port ${PORT}
    aliases: [kitchen/alias-clash]
peers:
  kitchen:
    proxy: http://kitchen
    models:
      - static
      - add .*
  garage:
    proxy: http://garage
    models: [shared]
`))
	require.NoError(t, err)

	// Copies share the index, as the router and server each hold one.
	copied := cfg

	assert.Equal(t, []string{"static"}, cfg.PeerModelIDs("kitchen"))

	dropped, changed := cfg.SetDiscoveredPeerModels("kitchen", []DiscoveredPeerModel{
		{ID: "static", Record: map[string]any{"id": "static", "context_length": float64(4096)}},
		{ID: "fresh", Record: map[string]any{"id": "fresh", "name": "Fresh"}},
		{ID: "shared"},
		{ID: "alias-clash"},
	})
	assert.True(t, changed)
	assert.Equal(t, []string{"kitchen/alias-clash"}, dropped)

	assert.Equal(t, []string{"static", "fresh", "shared"}, copied.PeerModelIDs("kitchen"))

	peerID, modelID, found := copied.ResolvePeerModel("fresh")
	assert.True(t, found)
	assert.Equal(t, "kitchen", peerID)
	assert.Equal(t, "fresh", modelID)

	// Two peers provide "shared", so only the qualified names route.
	_, _, found = copied.ResolvePeerModel("shared")
	assert.False(t, found)
	_, _, found = copied.ResolvePeerModel("kitchen/shared")
	assert.True(t, found)

	record, ok := copied.PeerModelRecord("kitchen", "static")
	assert.True(t, ok)
	assert.Equal(t, float64(4096), record["context_length"])

	_, changed = cfg.SetDiscoveredPeerModels("kitchen", []DiscoveredPeerModel{
		{ID: "static", Record: map[string]any{"id": "static", "context_length": float64(4096)}},
		{ID: "fresh", Record: map[string]any{"id": "fresh", "name": "Fresh"}},
		{ID: "shared"},
		{ID: "alias-clash"},
	})
	assert.False(t, changed, "same catalog again")

	_, changed = cfg.SetDiscoveredPeerModels("kitchen", nil)
	assert.True(t, changed)
	assert.Equal(t, []string{"static"}, copied.PeerModelIDs("kitchen"))
	_, _, found = copied.ResolvePeerModel("fresh")
	assert.False(t, found)
}

func TestConfig_DiscoveryPeerTargets(t *testing.T) {
	base := `
models:
  local:
    cmd: llama-server --port ${PORT}
peers:
  kitchen:
    proxy: http://kitchen
  garage:
    proxy: http://garage
    models: [known]
`
	t.Run("selector and pin may name a discovery peer model", func(t *testing.T) {
		_, err := LoadConfigFromReader(strings.NewReader(base + `
selectors:
  spill:
    strategy: spillover
    targets: [local, kitchen/later]
profiles:
  p:
    pins:
      fast: kitchen/later
`))
		require.NoError(t, err)
	})

	t.Run("unknown model on a static peer is still an error", func(t *testing.T) {
		_, err := LoadConfigFromReader(strings.NewReader(base + `
profiles:
  p:
    pins:
      fast: garage/unknown
`))
		require.Error(t, err)
	})

	t.Run("DiscoveryPeerFor", func(t *testing.T) {
		cfg, err := LoadConfigFromReader(strings.NewReader(base))
		require.NoError(t, err)
		peerID, modelID, ok := cfg.DiscoveryPeerFor("kitchen/org/model")
		assert.True(t, ok)
		assert.Equal(t, "kitchen", peerID)
		assert.Equal(t, "org/model", modelID)
		_, _, ok = cfg.DiscoveryPeerFor("garage/known")
		assert.False(t, ok)
		_, _, ok = cfg.DiscoveryPeerFor("kitchen/")
		assert.False(t, ok)
	})
}
