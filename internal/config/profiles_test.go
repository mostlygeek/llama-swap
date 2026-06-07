package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const profilesBaseModels = `
models:
  glm-5.1:
    cmd: path/to/cmd
    proxy: "http://localhost:8080"
    aliases:
      - llm-plan
    filters:
      setParamsByID:
        glm-thinking:
          reasoning: true
  qwen-3:
    cmd: path/to/cmd
    proxy: "http://localhost:8081"
  image-model:
    cmd: path/to/cmd
    proxy: "http://localhost:8082"
    aliases:
      - image-gen
`

func loadProfilesConfig(t *testing.T, extra string) (Config, error) {
	t.Helper()
	return LoadConfigFromReader(strings.NewReader(profilesBaseModels + extra))
}

func TestConfig_ProfileResolution(t *testing.T) {
	cfg, err := loadProfilesConfig(t, `
profiles:
  plan-smarter:
    description: "smarter planning"
    aliases:
      llm-plan: qwen-3
      image-gen: ~
`)
	require.NoError(t, err)

	overlay := cfg.Profiles["plan-smarter"].Aliases

	// Without a profile the static alias resolves to its own model.
	got, found := cfg.RealModelName("llm-plan")
	require.True(t, found)
	assert.Equal(t, "glm-5.1", got)

	// With the profile overlay it is remapped to qwen-3.
	got, found = cfg.RealModelNameWithProfile("llm-plan", overlay)
	require.True(t, found)
	assert.Equal(t, "qwen-3", got)

	// A disabled alias (~) is not found while the profile is active.
	_, found = cfg.RealModelNameWithProfile("image-gen", overlay)
	assert.False(t, found)

	// Model IDs always win over a profile alias of the same name is impossible
	// (load-time rejected), but unrelated model IDs still resolve.
	got, found = cfg.RealModelNameWithProfile("qwen-3", overlay)
	require.True(t, found)
	assert.Equal(t, "qwen-3", got)
}

func TestConfig_EffectiveRequestName(t *testing.T) {
	cfg, err := loadProfilesConfig(t, `
profiles:
  variant:
    aliases:
      llm-plan: glm-thinking
`)
	require.NoError(t, err)
	overlay := cfg.Profiles["variant"].Aliases

	// The effective request name is the profile target, so setParamsByID keys
	// match the variant.
	assert.Equal(t, "glm-thinking", cfg.EffectiveRequestName("llm-plan", overlay))
	// The variant key still resolves to the base model.
	got, found := cfg.RealModelNameWithProfile("llm-plan", overlay)
	require.True(t, found)
	assert.Equal(t, "glm-5.1", got)
	// Non-overridden names pass through unchanged.
	assert.Equal(t, "qwen-3", cfg.EffectiveRequestName("qwen-3", overlay))
}

func TestConfig_EffectiveAliasesFor(t *testing.T) {
	cfg, err := loadProfilesConfig(t, `
profiles:
  plan-smarter:
    aliases:
      llm-plan: qwen-3
`)
	require.NoError(t, err)
	overlay := cfg.Profiles["plan-smarter"].Aliases

	// llm-plan no longer resolves to glm-5.1 under the overlay.
	glmAliases := cfg.EffectiveAliasesFor("glm-5.1", overlay)
	assert.False(t, slices.Contains(glmAliases, "llm-plan"))

	// qwen-3 now gains llm-plan.
	qwenAliases := cfg.EffectiveAliasesFor("qwen-3", overlay)
	assert.True(t, slices.Contains(qwenAliases, "llm-plan"))

	// With no overlay the static aliases are returned unchanged.
	assert.Equal(t, cfg.Models["glm-5.1"].Aliases, cfg.EffectiveAliasesFor("glm-5.1", nil))
}

func TestConfig_ProfileValidation(t *testing.T) {
	cases := []struct {
		name    string
		extra   string
		wantErr string
	}{
		{
			name: "empty aliases map",
			extra: `
profiles:
  bad:
    description: "no aliases"
`,
			wantErr: "aliases map cannot be empty",
		},
		{
			name: "alias shadows model id",
			extra: `
profiles:
  bad:
    aliases:
      qwen-3: glm-5.1
`,
			wantErr: "conflicts with model ID",
		},
		{
			name: "unknown target",
			extra: `
profiles:
  bad:
    aliases:
      llm-plan: does-not-exist
`,
			wantErr: "is not a known model or alias",
		},
		{
			name: "legacy sequence shape rejected",
			extra: `
profiles:
  bad:
    - glm-5.1
`,
			wantErr: "legacy profiles format removed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadProfilesConfig(t, tc.extra)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestConfig_ProfileTargetCanBeAliasOrVariant(t *testing.T) {
	// Targets may be a model ID, a static alias, or a setParamsByID variant key.
	cfg, err := loadProfilesConfig(t, `
profiles:
  mixed:
    aliases:
      a: glm-5.1
      b: llm-plan
      c: glm-thinking
`)
	require.NoError(t, err)
	overlay := cfg.Profiles["mixed"].Aliases

	for _, alias := range []string{"a", "b", "c"} {
		got, found := cfg.RealModelNameWithProfile(alias, overlay)
		require.Truef(t, found, "alias %q should resolve", alias)
		assert.Equalf(t, "glm-5.1", got, "alias %q resolves to glm-5.1", alias)
	}
}
