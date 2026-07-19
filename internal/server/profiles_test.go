package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func profileTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
models:
  real:
    cmd: echo ${PORT}
    name: Real Model
    filters:
      setParamsByID:
        variant:
          thinking: true
  hidden:
    cmd: echo hidden
    proxy: http://localhost:1234
    name: Hidden Model
    unlisted: true
peers:
  remote:
    proxy: http://example.com
    models: [remote-model]
profiles:
  coding:
    description: Coding profile
    pins:
      public: variant
      disabled: ~
      real: hidden
      expose: hidden
      remote-model: hidden
`))
	require.NoError(t, err)
	return cfg
}

func profileTestServer(t *testing.T, cfg config.Config, local *stubRouter) *Server {
	t.Helper()
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = cfg
	s.routes()
	t.Cleanup(func() { s.store.Close() })
	return s
}

func TestServer_ProfileAPI(t *testing.T) {
	s := profileTestServer(t, profileTestConfig(t), newStubRouter([]string{"real", "hidden"}, "ok"))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/profiles", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var listed struct {
		Active   *string      `json:"active"`
		Profiles []apiProfile `json:"profiles"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	require.Nil(t, listed.Active)
	require.Len(t, listed.Profiles, 1)
	assert.Equal(t, "", listed.Profiles[0].Pins["disabled"])

	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/profiles/active", strings.NewReader(`{"name":"coding"}`)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "coding", s.ActiveProfile())

	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/profiles/active", strings.NewReader(`{"name":"missing"}`)))
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "coding", s.ActiveProfile())

	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/profiles/active", strings.NewReader(`{"name":null}`)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, s.ActiveProfile())
}

func TestServer_ProfileMiddleware_JSONAndFilters(t *testing.T) {
	cfg := profileTestConfig(t)
	local := newStubRouter([]string{"real", "hidden"}, "")
	var received shared.ReqContextData
	var body []byte
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		received, _ = shared.ReadContext(r.Context())
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}
	s := profileTestServer(t, cfg, local)
	_, err := s.setActiveProfile("coding")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	assert.Equal(t, "variant", received.Model)
	assert.Equal(t, "real", received.ModelID)
	assert.Empty(t, received.Metadata)
	assert.Equal(t, "variant", gjson.GetBytes(body, "model").String())
	assert.True(t, gjson.GetBytes(body, "thinking").Bool())

	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"disabled"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestServer_Profile_UpstreamPreservesBody(t *testing.T) {
	cfg := profileTestConfig(t)
	local := newStubRouter([]string{"real", "hidden"}, "")
	var gotBody string
	var gotModel string
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		data, _ := shared.ReadContext(r.Context())
		gotModel = data.ModelID
		w.WriteHeader(http.StatusOK)
	}
	s := profileTestServer(t, cfg, local)
	_, err := s.setActiveProfile("coding")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/upstream/public/custom", strings.NewReader(`{"model":"public"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, `{"model":"public"}`, gotBody)
	assert.Equal(t, "real", gotModel)
}

func TestServer_ProfileMiddleware_RewriteUpstreamPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		pins map[string]string
		want string
	}{
		{
			name: "pin",
			path: "public/v1/chat/completions",
			pins: map[string]string{"public": "real"},
			want: "real/v1/chat/completions",
		},
		{
			name: "longest pin",
			path: "author/public/v1/chat/completions",
			pins: map[string]string{"author": "short", "author/public": "real"},
			want: "real/v1/chat/completions",
		},
		{
			name: "disabled",
			path: "disabled/v1/chat/completions",
			pins: map[string]string{"disabled": ""},
			want: "",
		},
		{
			name: "not pinned",
			path: "real/v1/chat/completions",
			pins: map[string]string{"public": "real"},
			want: "real/v1/chat/completions",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/upstream/"+tc.path, nil)
			req.SetPathValue("upstreamPath", tc.path)
			model, replacement, found := upstreamProfilePin(tc.path, tc.pins)
			if found {
				var err error
				req, err = shared.ReplaceRequestModel(req, model, replacement)
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, req.PathValue("upstreamPath"))
			assert.Equal(t, "/upstream/"+tc.want, req.URL.Path)
		})
	}
}

func TestServer_Profile_UpstreamMetricsUseResolvedModel(t *testing.T) {
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
models:
  m1:
    cmd: echo ${PORT}
profiles:
  test:
    pins:
      public: m1
`))
	require.NoError(t, err)
	s := upstreamMetricsServer(t, `{"usage":{"prompt_tokens":2,"completion_tokens":3}}`)
	s.cfg = cfg
	s.routes()
	_, err = s.setActiveProfile("test")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/upstream/public/v1/chat/completions", strings.NewReader(`{"model":"public"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	entries := metricsEntries(t, s.metrics)
	require.Len(t, entries, 1)
	assert.Equal(t, "m1", entries[0].Model)
	assert.Empty(t, entries[0].Metadata)
}

func TestServer_Profile_ModelListings(t *testing.T) {
	s := profileTestServer(t, profileTestConfig(t), newStubRouter([]string{"real", "hidden"}, ""))
	_, err := s.setActiveProfile("coding")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	var response struct {
		Data []modelRecord `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	names := make(map[string]string)
	for _, record := range response.Data {
		names[record.ID] = record.Name
	}
	assert.Contains(t, names, "public")
	assert.Empty(t, names["public"])
	assert.Equal(t, "Real Model", names["real"])
	assert.Equal(t, "remote: remote-model", names["remote-model"])
	assert.Contains(t, names, "expose")
	assert.Empty(t, names["expose"])
	assert.NotContains(t, names, "disabled")
	assert.NotContains(t, names, "hidden")

	status := s.modelStatus()
	byID := make(map[string]apiModel)
	for _, model := range status {
		byID[model.Id] = model
	}
	assert.Contains(t, byID, "real")
	assert.Contains(t, byID, "hidden")
	assert.Equal(t, "remote", byID["remote-model"].PeerID)
	assert.NotContains(t, byID, "public")
	assert.NotContains(t, byID, "expose")
	assert.NotContains(t, byID, "disabled")
}

func TestServer_Profile_ManagementUsesConcreteModel(t *testing.T) {
	cfg := profileTestConfig(t)
	local := newStubRouter([]string{"real", "hidden"}, "")
	realLog := logmon.NewWriter(io.Discard)
	hiddenLog := logmon.NewWriter(io.Discard)
	local.loggers = map[string]*logmon.Monitor{
		"real":   realLog,
		"hidden": hiddenLog,
	}
	s := profileTestServer(t, cfg, local)
	_, err := s.setActiveProfile("coding")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/models/unload/real", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"real"}, local.unloadModels)

	logger, err := s.getLogger("real")
	require.NoError(t, err)
	assert.Same(t, realLog, logger)
}

func TestServer_Profile_ConcurrentSelectionAndListing(t *testing.T) {
	s := profileTestServer(t, profileTestConfig(t), newStubRouter([]string{"real", "hidden"}, ""))
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if worker%2 == 0 {
					_, _ = s.setActiveProfile("coding")
				} else {
					_, _ = s.setActiveProfile("")
				}
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
				assert.Equal(t, http.StatusOK, w.Code)
			}
		}(worker)
	}
	wg.Wait()
}
