package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Selectors_LoadDefaultsAndFields(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(`
models:
  a:
    cmd: echo ${PORT}
  b:
    cmd: echo ${PORT}
groups:
  balanced:
    swap: false
    members: [a, b]
selectors:
  coding:
    strategy: warm
    targets: [a, b]
    name: Coding Model
    description: Best available coding model
    unlisted: true
    metadata:
      family: coding
  workers:
    strategy: balance
    targets: [a, b]
`))
	require.NoError(t, err)

	coding := cfg.Selectors["coding"]
	assert.Equal(t, SelectorStrategyWarm, coding.Strategy)
	assert.Equal(t, []string{"a", "b"}, coding.Targets)
	assert.Equal(t, 1, coding.Balance.Spillover)
	assert.Equal(t, "Coding Model", coding.Name)
	assert.Equal(t, "Best available coding model", coding.Description)
	assert.True(t, coding.Unlisted)
	assert.Equal(t, "coding", coding.Metadata["family"])

	workers := cfg.Selectors["workers"]
	assert.Equal(t, 1, workers.Balance.Spillover)
}

func TestConfig_Selectors_Validation(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name: "empty selector id",
			config: `
selectors:
  " ":
    strategy: pin
    targets: [a]
`,
			wantErr: "selector names cannot be empty",
		},
		{
			name: "missing strategy",
			config: `
selectors:
  public:
    targets: [a]
`,
			wantErr: "strategy is required",
		},
		{
			name: "unknown strategy",
			config: `
selectors:
  public:
    strategy: random
    targets: [a]
`,
			wantErr: "unknown strategy",
		},
		{
			name: "missing targets",
			config: `
selectors:
  public:
    strategy: pin
`,
			wantErr: "targets must contain at least one entry",
		},
		{
			name: "unknown target",
			config: `
selectors:
  public:
    strategy: pin
    targets: [missing]
`,
			wantErr: `references unknown model "missing"`,
		},
		{
			name: "selector chaining",
			config: `
selectors:
  first:
    strategy: pin
    targets: [a]
  second:
    strategy: pin
    targets: [first]
`,
			wantErr: "selector chaining is not supported",
		},
		{
			name: "model id collision",
			config: `
selectors:
  a:
    strategy: pin
    targets: [a]
`,
			wantErr: "name conflicts with model ID",
		},
		{
			name: "explicit alias collision",
			config: `
selectors:
  alias-a:
    strategy: pin
    targets: [a]
`,
			wantErr: "name conflicts with model alias",
		},
		{
			name: "generated alias collision",
			config: `
selectors:
  variant:
    strategy: pin
    targets: [a]
`,
			wantErr: "name conflicts with model alias",
		},
		{
			name: "peer collision",
			config: `
selectors:
  remote-model:
    strategy: pin
    targets: [a]
`,
			wantErr: "name conflicts with peer model",
		},
		{
			name: "warm peer target",
			config: `
selectors:
  public:
    strategy: warm
    targets: [remote-model]
`,
			wantErr: `must resolve to a local model for strategy "warm"`,
		},
		{
			name: "balance peer target",
			config: `
selectors:
  public:
    strategy: balance
    targets: [remote-model]
`,
			wantErr: `must resolve to a local model for strategy "balance"`,
		},
		{
			name: "invalid spillover",
			config: `
selectors:
  public:
    strategy: balance
    targets: [a]
    balance:
      spillover: 0
`,
			wantErr: "spillover must be >= 1",
		},
		{
			name: "duplicate resolved balance target",
			config: `
selectors:
  public:
    strategy: balance
    targets: [a, alias-a]
`,
			wantErr: `duplicate resolved model "a"`,
		},
		{
			name: "balance swapping group",
			config: `
selectors:
  public:
    strategy: balance
    targets: [a, b]
`,
			wantErr: "must share a group with swap: false",
		},
	}

	const base = `
models:
  a:
    cmd: echo ${PORT}
    aliases: [alias-a]
    filters:
      setParamsByID:
        variant:
          temperature: 0
  b:
    cmd: echo ${PORT}
peers:
  remote:
    proxy: http://example.com
    models: [remote-model]
`

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(base + tc.config))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestConfig_Selectors_BalanceCoexistence(t *testing.T) {
	t.Run("group", func(t *testing.T) {
		cfg, err := LoadConfigFromReader(strings.NewReader(`
models:
  a:
    cmd: echo ${PORT}
  b:
    cmd: echo ${PORT}
groups:
  pool:
    swap: false
    members: [a, b]
selectors:
  public:
    strategy: balance
    targets: [a, b]
    balance:
      spillover: 4
`))
		require.NoError(t, err)
		assert.Equal(t, 4, cfg.Selectors["public"].Balance.Spillover)
	})

	t.Run("matrix common set", func(t *testing.T) {
		_, err := LoadConfigFromReader(strings.NewReader(`
models:
  a:
    cmd: echo ${PORT}
  b:
    cmd: echo ${PORT}
matrix:
  vars:
    A: a
    B: b
  sets:
    pool: "A & B"
selectors:
  public:
    strategy: balance
    targets: [a, b]
`))
		require.NoError(t, err)
	})

	t.Run("matrix separate sets", func(t *testing.T) {
		_, err := LoadConfigFromReader(strings.NewReader(`
models:
  a:
    cmd: echo ${PORT}
  b:
    cmd: echo ${PORT}
matrix:
  vars:
    A: a
    B: b
  sets:
    pool: "A | B"
selectors:
  public:
    strategy: balance
    targets: [a, b]
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must all appear together in one expanded matrix set")
	})
}

func TestConfig_Selectors_ProfileTargetsSelector(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(`
models:
  a:
    cmd: echo ${PORT}
selectors:
  public:
    strategy: pin
    targets: [a]
profiles:
  coding:
    pins:
      llm-code: public
`))
	require.NoError(t, err)
	assert.Equal(t, "public", cfg.Profiles["coding"].Pins["llm-code"])
}

func TestConfig_Selectors_MergeDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", `
models:
  a:
    cmd: echo ${PORT}
selectors:
  public:
    strategy: pin
    targets: [a]
`)
	writeYAML(t, dir, "b.yaml", `
selectors:
  public:
    strategy: pin
    targets: [a]
`)

	_, err := LoadConfigSources("", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate selectors "public"`)
}
