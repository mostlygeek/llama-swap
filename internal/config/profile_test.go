package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Profiles_LoadAndResolve(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(`
models:
  local:
    cmd: echo ${PORT}
    aliases: [local-fast]
    filters:
      setParamsByID:
        local-thinking:
          thinking: true
peers:
  remote:
    proxy: http://example.com
    models: [peer-model]
profiles:
  coding:
    description: Coding profile
    pins:
      direct: local
      alias: local-fast
      variant: local-thinking
      peer: peer-model
      disabled-empty: ""
      disabled-null: ~
      local: peer-model
`))
	require.NoError(t, err)

	profile := cfg.Profiles["coding"]
	assert.Equal(t, "Coding profile", profile.Description)
	assert.Equal(t, "", profile.Pins["disabled-empty"])
	assert.Equal(t, "", profile.Pins["disabled-null"])

	tests := []struct {
		requested string
		target    string
		modelID   string
		disabled  bool
	}{
		{"direct", "local", "local", false},
		{"alias", "local-fast", "local", false},
		{"variant", "local-thinking", "local", false},
		{"peer", "peer-model", "peer-model", false},
		{"local", "peer-model", "peer-model", false},
		{"disabled-empty", "", "", true},
		{"disabled-null", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.requested, func(t *testing.T) {
			target, pinned := profile.Pins[tc.requested]
			require.True(t, pinned)
			assert.Equal(t, tc.target, target)
			if tc.disabled {
				assert.Empty(t, target)
				return
			}
			modelID, found := cfg.ResolveBaseModel(target)
			require.True(t, found)
			assert.Equal(t, tc.modelID, modelID)
		})
	}

	got, found := cfg.ResolveBaseModel("local-fast")
	require.True(t, found)
	assert.Equal(t, "local", got)
}

func TestConfig_Profiles_Validation(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		wantErr string
	}{
		{
			name: "legacy list",
			profile: `profiles:
  old: [model]
`,
			wantErr: "legacy list syntax",
		},
		{
			name: "empty pins",
			profile: `profiles:
  empty:
    pins: {}
`,
			wantErr: "must contain at least one entry",
		},
		{
			name: "unknown target",
			profile: `profiles:
  bad:
    pins:
      public: missing
`,
			wantErr: "references unknown model",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(`
models:
  model:
    cmd: echo ${PORT}
` + tc.profile))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestConfig_Profiles_DoNotChain(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(`
models:
  base:
    cmd: echo ${PORT}
profiles:
  test:
    pins:
      first: base
      second: first
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `references unknown model "first"`)
	assert.Empty(t, cfg.Profiles)
}
