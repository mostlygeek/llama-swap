package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_PeerAliases_Load(t *testing.T) {
	t.Run("aliases load and are preserved", func(t *testing.T) {
		content := `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    apiKey: sk-test
    models:
      - meta-llama/llama-3.1-8b-instruct
    aliases:
      llama-3.1-8b: "meta-llama/llama-3.1-8b-instruct"
      llama-8b: "meta-llama/llama-3.1-8b-instruct"
`
		cfg, err := LoadConfigFromReader(strings.NewReader(content))
		require.NoError(t, err)
		require.Contains(t, cfg.Peers, "openrouter")

		aliases := cfg.Peers["openrouter"].Aliases
		require.Len(t, aliases, 2)
		assert.Equal(t, "meta-llama/llama-3.1-8b-instruct", aliases["llama-3.1-8b"])
		assert.Equal(t, "meta-llama/llama-3.1-8b-instruct", aliases["llama-8b"])
	})

}

func TestConfig_PeerAliases_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "alias collides with own peer models",
			yaml: `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    models:
      - shared-name
    aliases:
      shared-name: "real-upstream-model"
`,
			wantErr: `alias "shared-name" conflicts with an entry in peers.openrouter.models`,
		},
		{
			name: "alias collides with another peer model",
			yaml: `
peers:
  alpha:
    proxy: https://alpha.example.com
    models:
      - claimed
  beta:
    proxy: https://beta.example.com
    models:
      - beta-model
    aliases:
      claimed: "real-upstream"
`,
			wantErr: `alias "claimed" is already served by peer "alpha"`,
		},
		{
			name: "alias collides with another peer alias",
			yaml: `
peers:
  alpha:
    proxy: https://alpha.example.com
    models:
      - alpha-model
    aliases:
      shared-alias: "real-upstream-a"
  beta:
    proxy: https://beta.example.com
    models:
      - beta-model
    aliases:
      shared-alias: "real-upstream-b"
`,
			wantErr: `alias "shared-alias" is already served by peer "alpha"`,
		},
		{
			name: "alias collides with local model ID",
			yaml: `
models:
  local-model:
    cmd: server
    proxy: http://localhost:8080
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    models:
      - peer-model
    aliases:
      local-model: "real-upstream"
`,
			wantErr: `alias "local-model" conflicts with a local model ID`,
		},
		{
			name: "alias collides with local model alias",
			yaml: `
models:
  local-model:
    cmd: server
    proxy: http://localhost:8080
    aliases:
      - local-alias
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    models:
      - peer-model
    aliases:
      local-alias: "real-upstream"
`,
			wantErr: `alias "local-alias" conflicts with local alias of model "local-model"`,
		},
		{
			name: "empty alias value rejected",
			yaml: `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    models:
      - peer-model
    aliases:
      empty-alias: ""
`,
			wantErr: `empty upstream model name for alias "empty-alias"`,
		},
		{
			name: "empty alias key rejected",
			yaml: `
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    models:
      - peer-model
    aliases:
      "": "real-upstream"
`,
			wantErr: `empty alias key`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(tt.yaml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}


