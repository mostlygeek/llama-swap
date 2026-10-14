package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// splConfig loads a config that has one local model, one alias and one peer.
func splConfig(t *testing.T, extra string) config.Config {
	t.Helper()
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
models:
  qwen-coder:
    cmd: echo hi
    proxy: "http://localhost:9999"
    aliases: ["coder"]
    useModelName: "upstream-coder"
    filters:
      stripParams: "temperature"
      setParams:
        top_p: 0.9
      policy: |
        default temperature to 0.1
        set context.model_policy to "ran"
        set from_model to true

peers:
  remote:
    proxy: http://remote
    models: [m1]
    filters:
      policy: |
        set provider to {"zdr": true}
        deny 402 "payment required" when tier = "free"
` + extra))
	require.NoError(t, err)
	return cfg
}

// runJSON sends a JSON body through the filter middleware and returns the
// recorder plus what the downstream handler saw (nil body when not reached).
func runJSON(t *testing.T, cfg config.Config, body string, mutate func(*http.Request)) (*httptest.ResponseRecorder, []byte, swaputil.ReqContextData) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if mutate != nil {
		mutate(r)
	}

	var got []byte
	var ctx swaputil.ReqContextData
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		got = data
		ctx, _ = swaputil.ReadContext(r.Context())
		assert.Equal(t, int64(len(data)), r.ContentLength)
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	CreateFilterMiddleware(cfg)(final).ServeHTTP(rec, r)
	return rec, got, ctx
}

func TestServer_FilterMiddleware_SPL_LegacyFiltersRunFirst(t *testing.T) {
	cfg := splConfig(t, "")
	rec, got, _ := runJSON(t, cfg, `{"model":"qwen-coder","temperature":0.7}`, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	// stripParams removed temperature, so the policy's default filled it.
	assert.Equal(t, 0.1, gjson.GetBytes(got, "temperature").Float())
	assert.Equal(t, 0.9, gjson.GetBytes(got, "top_p").Float())
	assert.True(t, gjson.GetBytes(got, "from_model").Bool())
	assert.Equal(t, "upstream-coder", gjson.GetBytes(got, "model").String())
}

func TestServer_FilterMiddleware_SPL_GlobalHookBeforeModelPolicy(t *testing.T) {
	cfg := splConfig(t, `
hooks:
  on_request: |
    set from_hook to true
    set order to "hook"
    set temperature to 0.5
`)
	rec, got, _ := runJSON(t, cfg, `{"model":"qwen-coder"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, gjson.GetBytes(got, "from_hook").Bool())
	assert.True(t, gjson.GetBytes(got, "from_model").Bool())
	// The hook set temperature, so the model policy's default did not apply.
	assert.Equal(t, 0.5, gjson.GetBytes(got, "temperature").Float())
}

func TestServer_FilterMiddleware_SPL_HookDenyStopsModelPolicy(t *testing.T) {
	cfg := splConfig(t, `
hooks:
  on_request: |
    deny 400 "messages is required" when messages is missing
`)
	rec, got, _ := runJSON(t, cfg, `{"model":"qwen-coder"}`, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Nil(t, got, "downstream handler must not run after deny")

	rec, got, _ = runJSON(t, cfg, `{"model":"qwen-coder","messages":[]}`, nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, gjson.GetBytes(got, "from_model").Bool())
}

func TestServer_FilterMiddleware_SPL_DenyEnvelope(t *testing.T) {
	cfg := splConfig(t, `
hooks:
  on_request: |
    deny "Model is disabled" when request.model = "coder"
    deny 429 "slow down" when request.model = "qwen-coder"
`)

	rec, got, _ := runJSON(t, cfg, `{"model":"coder"}`, nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Nil(t, got)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var envelope struct {
		Src   string `json:"src"`
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, "llama-swap", envelope.Src)
	assert.Equal(t, "Model is disabled", envelope.Error.Message)
	assert.NotEmpty(t, envelope.Error.Type)

	rec, _, _ = runJSON(t, cfg, `{"model":"qwen-coder"}`, nil)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Contains(t, rec.Body.String(), "slow down")

	// Text clients get a plain message.
	rec, _, _ = runJSON(t, cfg, `{"model":"coder"}`, func(r *http.Request) {
		r.Header.Set("Accept", "text/plain")
	})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "llama-swap: Model is disabled", rec.Body.String())
}

func TestServer_FilterMiddleware_SPL_ContextLandsInMetadata(t *testing.T) {
	cfg := splConfig(t, `
hooks:
  on_request: |
    set context.tier to "gold" when auth.key = "secret"
    set context.session to "x" when request.header.x-session-id is present
`)
	rec, _, ctx := runJSON(t, cfg, `{"model":"qwen-coder"}`, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("X-Session-ID", "abc")
	})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "gold", ctx.Metadata["tier"])
	assert.Equal(t, "x", ctx.Metadata["session"])
	assert.Equal(t, "ran", ctx.Metadata["model_policy"])

	// Without the key the tier is never set.
	rec, _, ctx = runJSON(t, cfg, `{"model":"qwen-coder"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	_, hasTier := ctx.Metadata["tier"]
	assert.False(t, hasTier)
}

func TestServer_FilterMiddleware_SPL_UseModelNameVisible(t *testing.T) {
	cfg := splConfig(t, `
hooks:
  on_request: |
    set requested to "alias" when request.model = "coder"
    set body_model to "rewritten" when model = "upstream-coder"
`)
	rec, got, _ := runJSON(t, cfg, `{"model":"coder"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "alias", gjson.GetBytes(got, "requested").String())
	assert.Equal(t, "rewritten", gjson.GetBytes(got, "body_model").String())
	assert.Equal(t, "upstream-coder", gjson.GetBytes(got, "model").String())
}

func TestServer_FilterMiddleware_SPL_PeerPolicy(t *testing.T) {
	cfg := splConfig(t, "")

	rec, got, _ := runJSON(t, cfg, `{"model":"remote/m1"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, gjson.GetBytes(got, "provider.zdr").Bool())
	assert.False(t, gjson.GetBytes(got, "from_model").Exists(), "model policy must not run for a peer")

	rec, got, _ = runJSON(t, cfg, `{"model":"m1","tier":"free"}`, nil)
	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.Nil(t, got)
}

func TestServer_FilterMiddleware_SPL_NonJSONUntouched(t *testing.T) {
	cfg := splConfig(t, `
hooks:
  on_request: deny "always"
`)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("model=qwen-coder"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reached := false
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })
	rec := httptest.NewRecorder()
	CreateFilterMiddleware(cfg)(final).ServeHTTP(rec, r)
	assert.True(t, reached)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServer_FilterMiddleware_NoSPLConfigured(t *testing.T) {
	// A hand-built config has no compiled programs; legacy filters still run.
	cfg := config.Config{Models: map[string]config.ModelConfig{
		"m": {Filters: config.ModelFilters{Filters: config.Filters{StripParams: "temperature"}}},
	}}
	rec, got, _ := runJSON(t, cfg, `{"model":"m","temperature":1}`, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, gjson.GetBytes(got, "temperature").Exists())
	assert.Nil(t, cfg.SPL())
}

func TestServer_FilterMiddleware_SPL_UnknownModelPassesThrough(t *testing.T) {
	cfg := splConfig(t, `
hooks:
  on_request: deny "always"
`)
	// resolveFilters fails for an unknown model, so nothing runs and the
	// dispatcher downstream reports the 404.
	rec, got, _ := runJSON(t, cfg, `{"model":"nope"}`, nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `{"model":"nope"}`, string(got))
}
