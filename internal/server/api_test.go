package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

func TestServer_HandleListModels(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.cfg = config.Config{
		Models: map[string]config.ModelConfig{
			"visible": {Name: "Visible", Description: "a model"},
			"hidden":  {Unlisted: true},
		},
		Peers: config.PeerDictionaryConfig{
			"peer1": {Models: []string{"remote-model"}},
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Origin", "http://example.com")
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q", got)
	}

	var resp struct {
		Data []modelRecord `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	if !ids["visible"] || !ids["remote-model"] {
		t.Errorf("missing expected models: %v", ids)
	}
	if ids["hidden"] {
		t.Error("unlisted model should not appear")
	}
}

func TestServer_HandleListModels_Aliases(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.cfg = config.Config{
		IncludeAliasesInList: true,
		Models: map[string]config.ModelConfig{
			"real": {Aliases: []string{"nick"}},
		},
	}

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	var resp struct {
		Data []modelRecord `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	ids := map[string]bool{}
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	if !ids["real"] || !ids["nick"] {
		t.Errorf("expected alias entry; got %v", ids)
	}
}

func TestServer_HandleListModels_Status(t *testing.T) {
	local := newStubRouter(nil, "")
	local.running = map[string]process.ProcessState{"loaded-model": process.StateReady}
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{
		IncludeAliasesInList: true,
		Models: map[string]config.ModelConfig{
			"loaded-model":   {Aliases: []string{"loaded-alias"}},
			"unloaded-model": {},
		},
		Peers: config.PeerDictionaryConfig{
			"peer1": {Models: []string{"remote-model"}},
		},
	}

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	var resp struct {
		Data []modelRecord `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	statuses := map[string]string{}
	for _, m := range resp.Data {
		statuses[m.ID], _ = m.Status["value"].(string)
	}

	if statuses["loaded-model"] != "loaded" {
		t.Errorf("loaded-model status = %q, want loaded", statuses["loaded-model"])
	}
	if statuses["loaded-alias"] != "loaded" {
		t.Errorf("loaded-alias status = %q, want loaded", statuses["loaded-alias"])
	}
	if statuses["unloaded-model"] != "unloaded" {
		t.Errorf("unloaded-model status = %q, want unloaded", statuses["unloaded-model"])
	}
	if statuses["remote-model"] != "unloaded" {
		t.Errorf("remote-model status = %q, want unloaded", statuses["remote-model"])
	}
}

func TestServer_FindModelInPath(t *testing.T) {
	cfg := config.Config{Models: map[string]config.ModelConfig{
		"author":       {},
		"author/model": {},
		"simple":       {},
	}}

	cases := []struct {
		path      string
		wantName  string
		wantRem   string
		wantFound bool
	}{
		{"/simple/v1/chat", "simple", "/v1/chat", true},
		{"/author/model/v1/chat", "author/model", "/v1/chat", true},
		{"/author/model", "author/model", "/", true},
		{"/author/v1/chat", "author", "/v1/chat", true},
		{"/missing/v1", "", "", false},
		{"/", "", "", false},
	}
	for _, c := range cases {
		name, _, rem, found := shared.FindModelInPath(cfg, c.path)
		if found != c.wantFound || name != c.wantName || (found && rem != c.wantRem) {
			t.Errorf("FindModelInPath(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.path, name, rem, found, c.wantName, c.wantRem, c.wantFound)
		}
	}
}

func TestServer_HandleUpstream(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "upstream-body")
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{"m1": {}}}

	t.Run("proxies to local", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/m1/v1/chat", nil))
		if w.Code != http.StatusOK || w.Body.String() != "upstream-body" {
			t.Errorf("status=%d body=%q", w.Code, w.Body.String())
		}
	})

	t.Run("redirects bare model path", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/m1", nil))
		if w.Code != http.StatusMovedPermanently {
			t.Errorf("status = %d, want 301", w.Code)
		}
	})

	t.Run("unknown model 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/nope/v1", nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

func upstreamMetricsServer(t *testing.T, response string) *Server {
	t.Helper()
	cfg := config.Config{Models: map[string]config.ModelConfig{"m1": {}}}
	proxylog := logmon.NewWriter(io.Discard)
	s := &Server{
		cfg:         cfg,
		muxlog:      logmon.NewWriter(io.Discard),
		proxylog:    proxylog,
		upstreamlog: logmon.NewWriter(io.Discard),
		inflight:    newInflightTracker(),
		metrics:     newTestMetricsMonitor(t, proxylog, 10, 0),
		local:       newStubRouter([]string{"m1"}, response),
		peer:        newStubRouter(nil, ""),
	}
	s.routes()
	return s
}

func TestServer_HandleUpstream_IgnorePaths(t *testing.T) {
	// Compile a pattern that matches static asset suffixes.
	pattern := regexp.MustCompile(`.*\.(js|json|css|png|gif|jpg|jpeg|txt)$`)

	t.Run("matched path, model not loaded, returns 409", func(t *testing.T) {
		local := newStubRouter([]string{"m1"}, "upstream-body")
		// running is nil/empty: model is not in RunningModels() => not loaded.
		s := newTestServer(local, newStubRouter(nil, ""))
		s.cfg = config.Config{
			Models: map[string]config.ModelConfig{"m1": {}},
			Upstream: config.UpstreamConfig{
				IgnorePaths: []*regexp.Regexp{pattern},
			},
		}

		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/m1/foo.js", nil))

		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d (body=%q)", w.Code, http.StatusConflict, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "not loaded") {
			t.Errorf("body = %q, want it to contain 'not loaded'", w.Body.String())
		}
	})

	t.Run("matched path, model already loaded, serves normally", func(t *testing.T) {
		local := newStubRouter([]string{"m1"}, "upstream-body")
		local.running = map[string]process.ProcessState{"m1": process.StateReady}
		s := newTestServer(local, newStubRouter(nil, ""))
		s.cfg = config.Config{
			Models: map[string]config.ModelConfig{"m1": {}},
			Upstream: config.UpstreamConfig{
				IgnorePaths: []*regexp.Regexp{pattern},
			},
		}

		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/m1/foo.js", nil))

		if w.Code != http.StatusOK || w.Body.String() != "upstream-body" {
			t.Fatalf("status=%d body=%q, want 200 'upstream-body'", w.Code, w.Body.String())
		}
	})

	t.Run("non-matched path, model not loaded, serves normally", func(t *testing.T) {
		local := newStubRouter([]string{"m1"}, "upstream-body")
		s := newTestServer(local, newStubRouter(nil, ""))
		s.cfg = config.Config{
			Models: map[string]config.ModelConfig{"m1": {}},
			Upstream: config.UpstreamConfig{
				IgnorePaths: []*regexp.Regexp{pattern},
			},
		}

		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/m1/v1/chat/completions", nil))

		if w.Code != http.StatusOK || w.Body.String() != "upstream-body" {
			t.Fatalf("status=%d body=%q, want 200 'upstream-body'", w.Code, w.Body.String())
		}
	})

	t.Run("matched path, peer model, serves normally", func(t *testing.T) {
		// Peer routers do not appear via RunningModels on the local router;
		// they should fall through to normal dispatch without 409.
		local := newStubRouter(nil, "")
		peer := newStubRouter([]string{"m1"}, "peer-body")
		s := newTestServer(local, peer)
		s.cfg = config.Config{
			Models: map[string]config.ModelConfig{"m1": {}},
			Upstream: config.UpstreamConfig{
				IgnorePaths: []*regexp.Regexp{pattern},
			},
		}

		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/m1/foo.js", nil))

		if w.Code != http.StatusOK || w.Body.String() != "peer-body" {
			t.Fatalf("status=%d body=%q, want 200 'peer-body'", w.Code, w.Body.String())
		}
	})
}

func TestServer_HandleUpstream_MetricsRecordsSupportedPath(t *testing.T) {
	resp := `{"usage":{"prompt_tokens":3,"completion_tokens":5}}`
	s := upstreamMetricsServer(t, resp)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upstream/m1/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != resp {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	entries := metricsEntries(t, s.metrics)
	if len(entries) != 1 {
		t.Fatalf("want 1 metrics entry, got %d", len(entries))
	}
	if entries[0].Model != "m1" {
		t.Errorf("model = %q, want m1", entries[0].Model)
	}
	if entries[0].ReqPath != "/v1/chat/completions" {
		t.Errorf("req_path = %q, want /v1/chat/completions", entries[0].ReqPath)
	}
	if entries[0].Tokens.InputTokens != 3 || entries[0].Tokens.OutputTokens != 5 {
		t.Errorf("tokens = %+v, want input=3 output=5", entries[0].Tokens)
	}
}

func TestServer_HandleUpstream_MetricsSkipsUnsupportedPath(t *testing.T) {
	s := upstreamMetricsServer(t, "ok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upstream/m1/probe", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if len(metricsEntries(t, s.metrics)) != 0 {
		t.Errorf("want no metrics entries for unsupported path, got %d", len(metricsEntries(t, s.metrics)))
	}
}

func TestServer_HandleUpstream_MetricsSkipsGET(t *testing.T) {
	s := upstreamMetricsServer(t, `{"usage":{}}`)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream/m1/v1/chat/completions", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if len(metricsEntries(t, s.metrics)) != 0 {
		t.Errorf("want no metrics entries for GET upstream, got %d", len(metricsEntries(t, s.metrics)))
	}
}

func TestServer_HandleUpstream_InflightTracksSupportedPaths(t *testing.T) {
	for _, tc := range []struct {
		name     string
		method   string
		path     string
		wantPath string
	}{
		{
			name:     "post inference",
			method:   http.MethodPost,
			path:     "/upstream/m1/v1/chat/completions",
			wantPath: "/v1/chat/completions",
		},
		{
			name:     "get model endpoint",
			method:   http.MethodGet,
			path:     "/upstream/m1/props",
			wantPath: "/props",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := newStubRouter([]string{"m1"}, "ok")
			var s *Server
			var during shared.InFlightRequestsEvent
			local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
				during = s.inflight.Current()
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			}
			s = upstreamInflightServer(t, local)

			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)))
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
			}
			if len(during.Requests) != 1 {
				t.Fatalf("inflight during request = %+v, want 1 request", during)
			}
			entry := during.Requests[0]
			if entry.Model != "m1" || entry.Method != tc.method || entry.ReqPath != tc.wantPath {
				t.Errorf("inflight entry = %+v, want model=m1 method=%s path=%s", entry, tc.method, tc.wantPath)
			}
			if got := s.inflight.Current(); len(got.Requests) != 0 {
				t.Errorf("inflight after request = %d, want 0", len(got.Requests))
			}
		})
	}
}

func TestServer_HandleUpstream_InflightSkipsUnsupportedPath(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "ok")
	var s *Server
	var during shared.InFlightRequestsEvent
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		during = s.inflight.Current()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}
	s = upstreamInflightServer(t, local)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/upstream/m1/probe", strings.NewReader(`{}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if len(during.Requests) != 0 {
		t.Fatalf("inflight during unsupported path = %+v, want empty", during)
	}
}

func upstreamInflightServer(t *testing.T, local *stubRouter) *Server {
	t.Helper()
	cfg := config.Config{Models: map[string]config.ModelConfig{"m1": {}}}
	proxylog := logmon.NewWriter(io.Discard)
	s := &Server{
		cfg:         cfg,
		muxlog:      logmon.NewWriter(io.Discard),
		proxylog:    proxylog,
		upstreamlog: logmon.NewWriter(io.Discard),
		inflight:    newInflightTracker(),
		metrics:     newTestMetricsMonitor(t, proxylog, 10, 0),
		local:       local,
		peer:        newStubRouter(nil, ""),
	}
	s.routes()
	return s
}

func TestServer_HandleMetrics_Unavailable(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestServer_Redirects(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	for path, want := range map[string]string{"/": "/ui", "/upstream": "/ui/models"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusFound {
			t.Errorf("%s: status = %d, want 302", path, w.Code)
		}
		if got := w.Header().Get("Location"); got != want {
			t.Errorf("%s: Location = %q, want %q", path, got, want)
		}
	}
}

func TestServer_HandleListModels_Capabilities(t *testing.T) {
	newServer := func(mc config.ModelConfig) *Server {
		s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
		s.cfg = config.Config{Models: map[string]config.ModelConfig{"m": mc}}
		return s
	}
	getModel := func(t *testing.T, s *Server) modelRecord {
		t.Helper()
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
		var resp struct {
			Data []modelRecord `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Data) != 1 {
			t.Fatalf("expected 1 model, got %d", len(resp.Data))
		}
		return resp.Data[0]
	}

	t.Run("all_fields", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Capabilities: config.ModelCapConfig{
				In:      []string{"text", "image"},
				Out:     []string{"text", "audio"},
				Tools:   true,
				Context: 100000,
			},
		}))
		if m.Architecture == nil {
			t.Fatal("architecture is nil")
		}
		if !anySliceStrEqual(m.Architecture["input_modalities"], []string{"text", "image"}) {
			t.Errorf("input_modalities = %v", m.Architecture["input_modalities"])
		}
		if !anySliceStrEqual(m.Architecture["output_modalities"], []string{"text", "audio"}) {
			t.Errorf("output_modalities = %v", m.Architecture["output_modalities"])
		}
		if m.Architecture["modality"] != "text+image->text+audio" {
			t.Errorf("modality = %v", m.Architecture["modality"])
		}
		if m.Capabilities == nil || m.Capabilities["vision"] != true {
			t.Errorf("vision = %v", m.Capabilities)
		}
		if m.Capabilities["audio_speech"] != true {
			t.Errorf("audio_speech = %v", m.Capabilities["audio_speech"])
		}
		if m.Capabilities["function_calling"] != true {
			t.Errorf("function_calling = %v", m.Capabilities["function_calling"])
		}
		if !stringSliceEqual(m.SupportedParameters, []string{"tools", "tool_choice"}) {
			t.Errorf("supported_parameters = %v", m.SupportedParameters)
		}
		if m.ContextLength != 100000 {
			t.Errorf("context_length = %d", m.ContextLength)
		}
	})

	t.Run("in_only", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Capabilities: config.ModelCapConfig{In: []string{"text", "image"}},
		}))
		if m.Architecture == nil {
			t.Fatal("architecture is nil")
		}
		if _, ok := m.Architecture["output_modalities"]; ok {
			t.Error("should not have output_modalities")
		}
		if _, ok := m.Architecture["modality"]; ok {
			t.Error("should not have modality")
		}
		if m.Capabilities == nil || m.Capabilities["vision"] != true {
			t.Error("expected vision: true")
		}
		if m.SupportedParameters != nil {
			t.Error("should not have supported_parameters")
		}
		if m.ContextLength != 0 {
			t.Error("should not have context_length")
		}
	})

	t.Run("out_only", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Capabilities: config.ModelCapConfig{Out: []string{"audio"}},
		}))
		if m.Architecture == nil {
			t.Fatal("architecture is nil")
		}
		if _, ok := m.Architecture["input_modalities"]; ok {
			t.Error("should not have input_modalities")
		}
		if len(m.Capabilities) > 0 {
			t.Errorf("expected no capabilities, got %v", m.Capabilities)
		}
	})

	t.Run("tools", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Capabilities: config.ModelCapConfig{Tools: true},
		}))
		if m.Capabilities == nil || m.Capabilities["function_calling"] != true {
			t.Error("expected function_calling: true")
		}
		if !stringSliceEqual(m.SupportedParameters, []string{"tools", "tool_choice"}) {
			t.Errorf("supported_parameters = %v", m.SupportedParameters)
		}
		if m.Architecture != nil {
			t.Error("should not have architecture")
		}
	})

	t.Run("reranker", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Capabilities: config.ModelCapConfig{Reranker: true},
		}))
		if m.Capabilities == nil || m.Capabilities["reranker"] != true {
			t.Error("expected reranker: true")
		}
		if m.Architecture != nil {
			t.Error("should not have architecture")
		}
	})

	t.Run("context", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Capabilities: config.ModelCapConfig{Context: 32768},
		}))
		if m.ContextLength != 32768 {
			t.Errorf("context_length = %d", m.ContextLength)
		}
		if m.Architecture != nil {
			t.Error("should not have architecture")
		}
	})

	t.Run("audio_transcriptions", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Capabilities: config.ModelCapConfig{In: []string{"audio"}, Out: []string{"text"}},
		}))
		if m.Capabilities == nil || m.Capabilities["audio_transcriptions"] != true {
			t.Error("expected audio_transcriptions: true")
		}
	})

	t.Run("image_generation", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Capabilities: config.ModelCapConfig{In: []string{"text"}, Out: []string{"image"}},
		}))
		if m.Capabilities == nil || m.Capabilities["image_generation"] != true {
			t.Error("expected image_generation: true")
		}
	})

	t.Run("image_to_image", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Capabilities: config.ModelCapConfig{In: []string{"image"}, Out: []string{"image"}},
		}))
		if m.Capabilities == nil || m.Capabilities["image_to_image"] != true {
			t.Error("expected image_to_image: true")
		}
	})

	t.Run("empty_skip", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{}))
		if m.Architecture != nil {
			t.Error("should not have architecture")
		}
		if m.Capabilities != nil {
			t.Error("should not have capabilities")
		}
		if m.SupportedParameters != nil {
			t.Error("should not have supported_parameters")
		}
		if m.ContextLength != 0 {
			t.Error("should not have context_length")
		}
	})

	t.Run("metadata_precedence", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Capabilities: config.ModelCapConfig{In: []string{"text"}},
			Metadata: map[string]any{
				"architecture":   "should-be-dropped",
				"custom_field":   "should-remain",
				"capabilities":   "also-dropped",
				"other_metadata": "also-remain",
			},
		}))
		if m.Architecture == nil || m.Architecture["input_modalities"] == nil {
			t.Fatal("architecture should be rendered, not from metadata")
		}
		if m.Meta == nil || m.Meta["llamaswap"] == nil {
			t.Fatal("meta.llamaswap should exist")
		}
		meta := m.Meta["llamaswap"].(map[string]any)
		if _, ok := meta["architecture"]; ok {
			t.Error("architecture should be filtered from metadata")
		}
		if _, ok := meta["custom_field"]; !ok {
			t.Error("custom_field should remain in metadata")
		}
	})

	t.Run("metadata_passthrough_no_caps", func(t *testing.T) {
		m := getModel(t, newServer(config.ModelConfig{
			Metadata: map[string]any{
				"architecture":   "preserved",
				"context_length": 4096,
				"capabilities":   "preserved",
				"custom_field":   "preserved",
			},
		}))
		if m.Architecture != nil {
			t.Error("should not have architecture when caps is empty")
		}
		if m.Meta == nil || m.Meta["llamaswap"] == nil {
			t.Fatal("meta.llamaswap should exist")
		}
		meta := m.Meta["llamaswap"].(map[string]any)
		if _, ok := meta["architecture"]; !ok {
			t.Error("architecture should be preserved in metadata when caps is empty")
		}
		if _, ok := meta["context_length"]; !ok {
			t.Error("context_length should be preserved in metadata when caps is empty")
		}
	})
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func anySliceStrEqual(v any, want []string) bool {
	arr, ok := v.([]any)
	if !ok {
		return false
	}
	if len(arr) != len(want) {
		return false
	}
	for i := range arr {
		if s, ok := arr[i].(string); !ok || s != want[i] {
			return false
		}
	}
	return true
}
