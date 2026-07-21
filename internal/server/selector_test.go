package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func selectorTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
models:
  a:
    cmd: echo ${PORT}
    aliases: [alias-a]
    filters:
      setParamsByID:
        variant:
          thinking: true
  b:
    cmd: echo ${PORT}
  c:
    cmd: echo ${PORT}
groups:
  balanced:
    swap: false
    members: [a, b, c]
peers:
  remote:
    proxy: http://example.com
    models: [remote-model]
selectors:
  public:
    strategy: pin
    targets: [variant, remote-model]
    name: Public Model
    description: Public selector
    metadata:
      purpose: testing
  warm:
    strategy: warm
    targets: [a, b, c]
  balanced:
    strategy: spillover
    targets: [a, b, c]
    settings:
      spillover: 1
  hidden-selector:
    strategy: pin
    targets: [a]
    unlisted: true
profiles:
  coding:
    pins:
      llm-code: public
`))
	require.NoError(t, err)
	return cfg
}

func selectorTestServer(t *testing.T, cfg config.Config, local *stubRouter) *Server {
	t.Helper()
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = cfg
	s.routes()
	t.Cleanup(func() { s.store.Close() })
	return s
}

func TestServer_SelectorStrategyPin(t *testing.T) {
	target, err := strategyPin(config.SelectorConfig{Targets: []string{"first", "second"}})
	require.NoError(t, err)
	assert.Equal(t, "first", target)
}

func TestServer_SelectorStrategyWarm(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"a": {},
			"b": {},
			"c": {},
		},
	}
	selector := config.SelectorConfig{Targets: []string{"a", "b", "c"}}

	target, err := strategyWarm(cfg, selector, map[string]process.ProcessState{
		"a": process.StateStarting,
		"c": process.StateReady,
	})
	require.NoError(t, err)
	assert.Equal(t, "c", target)

	target, err = strategyWarm(cfg, selector, map[string]process.ProcessState{
		"a": process.StateStarting,
		"b": process.StateReady,
		"c": process.StateReady,
	})
	require.NoError(t, err)
	assert.Equal(t, "b", target)

	target, err = strategyWarm(cfg, selector, map[string]process.ProcessState{
		"a": process.StateStarting,
		"b": process.StateStarting,
	})
	require.NoError(t, err)
	assert.Equal(t, "a", target)

	target, err = strategyWarm(cfg, selector, nil)
	require.NoError(t, err)
	assert.Equal(t, "a", target)
}

func TestServer_SelectorStrategySpillover(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"a": {},
			"b": {},
			"c": {},
		},
		Selectors: map[string]config.SelectorConfig{
			"pool": {
				Strategy: config.SelectorStrategySpillover,
				Targets:  []string{"a", "b", "c"},
				Settings: config.SelectorSettings{Spillover: 2},
			},
		},
	}
	tracker := newSelectorSpilloverTracker(cfg)

	first, err := strategySpillover("pool", tracker, nil)
	require.NoError(t, err)
	second, err := strategySpillover("pool", tracker, nil)
	require.NoError(t, err)
	third, err := strategySpillover("pool", tracker, nil)
	require.NoError(t, err)
	assert.Equal(t, "a", first)
	assert.Equal(t, "a", second)
	assert.Equal(t, "b", third)

	tracker.release("pool", "a")
	target, err := strategySpillover("pool", tracker, map[string]process.ProcessState{
		"a": process.StateReady,
		"b": process.StateReady,
		"c": process.StateReady,
	})
	require.NoError(t, err)
	assert.Equal(t, "c", target)

	stoppedTracker := newSelectorSpilloverTracker(cfg)
	_, err = strategySpillover("pool", stoppedTracker, map[string]process.ProcessState{
		"a": process.StateStopping,
		"b": process.StateStopping,
		"c": process.StateShutdown,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no available spillover targets")
}

func TestServer_SelectorMiddleware_RewritesBeforeFiltersAndRecordsActivity(t *testing.T) {
	cfg := selectorTestConfig(t)
	local := newStubRouter([]string{"a", "b", "c"}, "")
	var received shared.ReqContextData
	var body []byte
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		received, _ = shared.ReadContext(r.Context())
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"usage":{"prompt_tokens":2,"completion_tokens":3}}`))
	}
	s := selectorTestServer(t, cfg, local)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("public"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	assert.Equal(t, "variant", received.Model)
	assert.Equal(t, "a", received.ModelID)
	assert.Equal(t, "variant", gjson.GetBytes(body, "model").String())
	assert.True(t, gjson.GetBytes(body, "thinking").Bool())

	entries := metricsEntries(t, s.metrics)
	require.Len(t, entries, 1)
	assert.Equal(t, "a", entries[0].Model)
	assert.Equal(t, "public", entries[0].Metadata["selector"])
}

func TestServer_SelectorMiddleware_ProfileRunsFirst(t *testing.T) {
	cfg := selectorTestConfig(t)
	local := newStubRouter([]string{"a", "b", "c"}, "")
	var received shared.ReqContextData
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		received, _ = shared.ReadContext(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"usage":{}}`))
	}
	s := selectorTestServer(t, cfg, local)
	_, err := s.setActiveProfile("coding")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("llm-code"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "a", received.ModelID)

	entries := metricsEntries(t, s.metrics)
	require.Len(t, entries, 1)
	assert.Equal(t, "public", entries[0].Metadata["selector"])
}

func TestServer_SelectorMiddleware_SpilloverReservations(t *testing.T) {
	cfg := selectorTestConfig(t)
	local := newStubRouter([]string{"a", "b", "c"}, "")
	local.running = map[string]process.ProcessState{
		"a": process.StateReady,
		"b": process.StateReady,
		"c": process.StateReady,
	}
	selected := make(chan string, 2)
	release := make(chan struct{})
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		data, _ := shared.ReadContext(r.Context())
		selected <- data.ModelID
		<-release
		w.WriteHeader(http.StatusOK)
	}
	s := selectorTestServer(t, cfg, local)

	done := make(chan struct{}, 2)
	go func() {
		s.ServeHTTP(httptest.NewRecorder(), chatRequest("balanced"))
		done <- struct{}{}
	}()
	first := <-selected

	go func() {
		s.ServeHTTP(httptest.NewRecorder(), chatRequest("balanced"))
		done <- struct{}{}
	}()
	second := <-selected

	assert.NotEqual(t, first, second)
	close(release)
	<-done
	<-done
}

func TestServer_SelectorMiddleware_AllSpilloverTargetsStopping(t *testing.T) {
	cfg := selectorTestConfig(t)
	local := newStubRouter([]string{"a", "b", "c"}, "")
	local.running = map[string]process.ProcessState{
		"a": process.StateStopping,
		"b": process.StateStopping,
		"c": process.StateShutdown,
	}
	s := selectorTestServer(t, cfg, local)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("balanced"))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "no available spillover targets")
}

func TestServer_SelectorMiddleware_UpstreamUnsupported(t *testing.T) {
	cfg := selectorTestConfig(t)
	local := newStubRouter([]string{"a", "b", "c"}, "")
	called := false
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}
	s := selectorTestServer(t, cfg, local)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upstream/public/v1/chat/completions", strings.NewReader(`{"model":"public"}`))
	req.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.False(t, called)
}

func TestServer_Selector_ModelListings(t *testing.T) {
	cfg := selectorTestConfig(t)
	local := newStubRouter([]string{"a", "b", "c"}, "")
	local.running = map[string]process.ProcessState{"a": process.StateStarting}
	s := selectorTestServer(t, cfg, local)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var response struct {
		Data []modelRecord `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	byID := make(map[string]modelRecord)
	for _, record := range response.Data {
		byID[record.ID] = record
	}

	public, found := byID["public"]
	require.True(t, found)
	assert.Equal(t, "Public Model", public.Name)
	assert.Equal(t, "Public selector", public.Description)
	assert.Equal(t, "loaded", public.Status["value"])
	require.Contains(t, public.Meta, "llamaswap")
	metadata, ok := public.Meta["llamaswap"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "testing", metadata["purpose"])
	assert.Equal(t, "selector", metadata["type"])
	assert.NotContains(t, byID, "hidden-selector")
}

func TestServer_Selector_ModelListingPinUsesFirstTarget(t *testing.T) {
	cfg := selectorTestConfig(t)
	selector := cfg.Selectors["public"]
	selector.Targets = []string{"a", "b"}
	cfg.Selectors["public"] = selector

	local := newStubRouter([]string{"a", "b", "c"}, "")
	local.running = map[string]process.ProcessState{"b": process.StateReady}
	s := selectorTestServer(t, cfg, local)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var response struct {
		Data []modelRecord `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	for _, record := range response.Data {
		if record.ID == "public" {
			assert.Equal(t, "unloaded", record.Status["value"])
			return
		}
	}
	t.Fatal("public selector missing from model listing")
}
