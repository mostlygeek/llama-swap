package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mostlygeek/llama-swap/internal/spl"
)

func TestConfig_SPL_Valid(t *testing.T) {
	content := `
policies:
  coding-defaults: |
    default temperature to 0.2
    set context.workload to "coding"
  private-model: |
    deny "Login required"
      when auth.key is missing
    apply coding-defaults

hooks:
  on_request: |
    deny 400 "messages is required"
      when messages is missing
    apply private-model
      when request.model matches /^private-/

models:
  qwen-coder:
    cmd: echo hi
    proxy: "http://localhost:9999"
    filters:
      policy: |
        remove reasoning_effort
        set context.model_id to "${MODEL_ID}"
  plain:
    cmd: echo hi
    proxy: "http://localhost:9999"
    filters:
      policy: "   "

peers:
  remote:
    proxy: http://remote
    models: [m1]
    filters:
      policy: |
        set provider to {"zdr": true}
`
	cfg, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)

	programs := cfg.SPL()
	require.NotNil(t, programs)
	assert.Equal(t, []string{"coding-defaults", "private-model"}, programs.Library.Names())
	require.NotNil(t, programs.OnRequest)
	assert.Equal(t, 2, programs.OnRequest.Len())

	require.Contains(t, programs.Models, "qwen-coder")
	assert.NotContains(t, programs.Models, "plain", "blank policies are not compiled")
	require.Contains(t, programs.Peers, "remote")

	// ${MODEL_ID} was expanded inside the model policy.
	req := &spl.Request{Body: []byte(`{"reasoning_effort":"high"}`)}
	denied, err := programs.Models["qwen-coder"].Run(programs.Library, req)
	require.NoError(t, err)
	assert.Nil(t, denied)
	assert.Equal(t, `{}`, string(req.Body))
	assert.Equal(t, "qwen-coder", req.Context["model_id"])

	// The hook applies policies from the library.
	req = &spl.Request{Body: []byte(`{"messages":[]}`), Model: "private-x"}
	denied, err = programs.OnRequest.Run(programs.Library, req)
	require.NoError(t, err)
	require.NotNil(t, denied)
	assert.Equal(t, 403, denied.Status)
	assert.Equal(t, "Login required", denied.Message)

	req = &spl.Request{Body: []byte(`{"messages":[]}`), Model: "private-x", APIKey: "k"}
	denied, err = programs.OnRequest.Run(programs.Library, req)
	require.NoError(t, err)
	assert.Nil(t, denied)
	assert.Equal(t, 0.2, gjson.GetBytes(req.Body, "temperature").Float())
	assert.Equal(t, "coding", req.Context["workload"])
}

func TestConfig_SPL_NoSPLLeavesNil(t *testing.T) {
	content := `
models:
  m:
    cmd: echo hi
    proxy: "http://localhost:9999"
    filters:
      policy: ""
hooks:
  on_request: ""
`
	cfg, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)
	assert.Nil(t, cfg.SPL())
	assert.Nil(t, Config{}.SPL())
}

func TestConfig_SPL_Errors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "policy parse error location",
			content: `
policies:
  bad: |
    remove a
    set b 1
`,
			want: `policies.bad: line 2:7: unexpected "1", expected 'to'`,
		},
		{
			name: "hook parse error",
			content: `
hooks:
  on_request: |
    deny "x" when a matches /(/
`,
			want: "hooks.on_request: line 1:25: invalid regex:",
		},
		{
			name: "model policy parse error",
			content: `
models:
  qwen:
    cmd: echo hi
    proxy: "http://localhost:9999"
    filters:
      policy: |
        sett a to 1
`,
			want: `model qwen: filters.policy: line 1:1: unexpected "sett", expected an action`,
		},
		{
			name: "peer policy parse error",
			content: `
peers:
  remote:
    proxy: http://remote
    models: [m1]
    filters:
      policy: |
        remove a
        deny 200 "x"
`,
			want: "peer remote: filters.policy: line 2:6: deny status must be an integer between 400 and 599",
		},
		{
			name: "unknown policy in library",
			content: `
policies:
  a: |
    apply zzz
`,
			want: `policies.a: line 1:7: apply references unknown policy "zzz"`,
		},
		{
			name: "unknown policy in hook",
			content: `
hooks:
  on_request: apply nope
`,
			want: `hooks.on_request: line 1:7: apply references unknown policy "nope"`,
		},
		{
			name: "unknown policy in model without any policies",
			content: `
models:
  qwen:
    cmd: echo hi
    proxy: "http://localhost:9999"
    filters:
      policy: apply nope
`,
			want: `model qwen: filters.policy: line 1:7: apply references unknown policy "nope"`,
		},
		{
			name: "cycle",
			content: `
policies:
  a: apply b
  b: apply c
  c: apply a
`,
			want: "policies: cycle detected: a -> b -> c -> a",
		},
		{
			name: "protected model in policy",
			content: `
policies:
  p: |
    set model to "other"
`,
			want: `policies.p: line 1:5: cannot set protected parameter "model"`,
		},
		{
			name: "protected model in model policy",
			content: `
models:
  qwen:
    cmd: echo hi
    proxy: "http://localhost:9999"
    filters:
      policy: remove model
`,
			want: `model qwen: filters.policy: line 1:8: cannot remove protected parameter "model"`,
		},
		{
			name: "invalid policy name",
			content: `
policies:
  "bad name": remove a
`,
			want: `policies: invalid policy name "bad name"`,
		},
		{
			name: "unknown macro in policy",
			content: `
policies:
  p: |
    set a to "${nope}"
`,
			want: "unknown macro '${nope}'",
		},
		{
			name: "unknown macro in model policy",
			content: `
models:
  qwen:
    cmd: echo hi
    proxy: "http://localhost:9999"
    filters:
      policy: |
        set a to "${nope}"
`,
			want: "unknown macro '${nope}'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(tt.content))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestConfig_SPL_MacroSubstitution(t *testing.T) {
	content := `
macros:
  default_temp: 0.3
  tag: "global"

policies:
  defaults: |
    default temperature to ${default_temp}
    set context.tag to "${tag}"

hooks:
  on_request: |
    apply defaults

models:
  qwen:
    cmd: echo hi
    proxy: "http://localhost:9999"
    macros:
      tag: "model-local"
    filters:
      policy: |
        set context.model to "${MODEL_ID}"
        set context.local_tag to "${tag}"
`
	cfg, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)
	programs := cfg.SPL()
	require.NotNil(t, programs)

	req := &spl.Request{Body: []byte(`{}`)}
	_, err = programs.OnRequest.Run(programs.Library, req)
	require.NoError(t, err)
	assert.Equal(t, 0.3, gjson.GetBytes(req.Body, "temperature").Float())
	assert.Equal(t, "global", req.Context["tag"])

	req = &spl.Request{Body: []byte(`{}`)}
	_, err = programs.Models["qwen"].Run(programs.Library, req)
	require.NoError(t, err)
	assert.Equal(t, "qwen", req.Context["model"])
	assert.Equal(t, "model-local", req.Context["local_tag"])
}

func TestLoadConfigSources_DuplicatePolicy(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", "policies:\n  shared: remove a\nmodels:\n"+modelCfg("alpha", "echo a"))
	writeYAML(t, dir, "b.yaml", "policies:\n  shared: remove b\n")

	_, err := LoadConfigSources("", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate policies "shared"`)

	// Distinct names across files merge.
	dir = t.TempDir()
	writeYAML(t, dir, "a.yaml", "policies:\n  one: remove a\nmodels:\n"+modelCfg("alpha", "echo a"))
	writeYAML(t, dir, "b.yaml", "policies:\n  two: apply one\n")
	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, cfg.SPL().Library.Names())
}
