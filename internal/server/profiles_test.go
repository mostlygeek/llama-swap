package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/router"
)

// echoRouter dispatches every handled model and echoes the resolved model ID
// from the request context, so tests can assert which model a request was
// routed to after the profile overlay runs.
type echoRouter struct{ handled map[string]bool }

func newEchoRouter(models ...string) *echoRouter {
	m := make(map[string]bool, len(models))
	for _, id := range models {
		m[id] = true
	}
	return &echoRouter{handled: m}
}

func (e *echoRouter) Handles(model string) bool      { return e.handled[model] }
func (e *echoRouter) Shutdown(_ time.Duration) error { return nil }
func (e *echoRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, _ := router.ReadContext(r.Context())
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(data.ModelID))
}
func (e *echoRouter) RunningModels() map[string]process.ProcessState { return nil }
func (e *echoRouter) Unload(_ time.Duration, _ ...string)            {}
func (e *echoRouter) ProcessLogger(_ string) (*logmon.Monitor, bool) { return nil, false }

const profileServerConfig = `
models:
  glm-5.1:
    cmd: path/to/cmd
    proxy: "http://localhost:8080"
    aliases:
      - llm-plan
  qwen-3:
    cmd: path/to/cmd
    proxy: "http://localhost:8081"
  image-model:
    cmd: path/to/cmd
    proxy: "http://localhost:8082"
    aliases:
      - image-gen
includeAliasesInList: true
profiles:
  plan-smarter:
    description: "smarter planning; no image gen"
    aliases:
      llm-plan: qwen-3
      image-gen: ~
`

// profileServerMatrixConfig declares the same models and profiles but routes
// through a matrix instead of groups, to prove the profile overlay is a layer
// in front of routing and behaves identically regardless of the backend.
const profileServerMatrixConfig = `
models:
  glm-5.1:
    cmd: path/to/cmd
    proxy: "http://localhost:8080"
    aliases:
      - llm-plan
  qwen-3:
    cmd: path/to/cmd
    proxy: "http://localhost:8081"
  image-model:
    cmd: path/to/cmd
    proxy: "http://localhost:8082"
    aliases:
      - image-gen
includeAliasesInList: true
matrix:
  vars:
    a: glm-5.1
    b: qwen-3
    c: image-model
  sets:
    s: "a | b | c"
profiles:
  plan-smarter:
    description: "smarter planning; no image gen"
    aliases:
      llm-plan: qwen-3
      image-gen: ~
`

func newProfileServer(t *testing.T, local router.LocalRouter) *Server {
	t.Helper()
	return newProfileServerFrom(t, profileServerConfig, local)
}

func newProfileServerFrom(t *testing.T, configYAML string, local router.LocalRouter) *Server {
	t.Helper()
	cfg, err := config.LoadConfigFromReader(strings.NewReader(configYAML))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	proxylog := logmon.NewWriter(io.Discard)
	s := &Server{
		cfg:         cfg,
		muxlog:      logmon.NewWriter(io.Discard),
		proxylog:    proxylog,
		upstreamlog: logmon.NewWriter(io.Discard),
		inflight:    &inflightCounter{},
		metrics:     newMetricsMonitor(proxylog, 0, 0),
		local:       local,
		peer:        newStubRouter(nil, ""),
		shutdownCtx: ctx,
		shutdownFn:  cancel,
	}
	s.routes()
	return s
}

func TestServer_ProfileRewritesModelBeforeDispatch(t *testing.T) {
	s := newProfileServer(t, newEchoRouter("glm-5.1", "qwen-3", "image-model"))

	// Without a profile, the static alias dispatches to glm-5.1.
	w := httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("llm-plan"))
	if w.Code != http.StatusOK || w.Body.String() != "glm-5.1" {
		t.Fatalf("no profile: status=%d body=%q want glm-5.1", w.Code, w.Body.String())
	}

	// Activate the profile: the same alias now dispatches to qwen-3.
	if err := s.SetActiveProfile("plan-smarter"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("llm-plan"))
	if w.Code != http.StatusOK || w.Body.String() != "qwen-3" {
		t.Fatalf("profile active: status=%d body=%q want qwen-3", w.Code, w.Body.String())
	}

	// A disabled alias (~) is not found while the profile is active.
	w = httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("image-gen"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled alias: status=%d want 404", w.Code)
	}

	// Clearing the profile restores default resolution.
	if err := s.SetActiveProfile(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, chatRequest("image-gen"))
	if w.Code != http.StatusOK || w.Body.String() != "image-model" {
		t.Fatalf("after clear: status=%d body=%q want image-model", w.Code, w.Body.String())
	}
}

func TestServer_ProfileListEndpoint(t *testing.T) {
	s := newProfileServer(t, newEchoRouter("glm-5.1", "qwen-3", "image-model"))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/profiles", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Active   string       `json:"active"`
		Profiles []apiProfile `json:"profiles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Active != "" {
		t.Errorf("active=%q want empty", body.Active)
	}
	if len(body.Profiles) != 1 || body.Profiles[0].Name != "plan-smarter" {
		t.Fatalf("profiles=%+v", body.Profiles)
	}
	if body.Profiles[0].Description != "smarter planning; no image gen" {
		t.Errorf("description=%q", body.Profiles[0].Description)
	}
}

func TestServer_ProfileActivateEndpoint(t *testing.T) {
	s := newProfileServer(t, newEchoRouter("glm-5.1", "qwen-3", "image-model"))

	// Activate a known profile.
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/profiles/activate/plan-smarter", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("activate status=%d", w.Code)
	}
	if name, _ := s.ActiveProfile(); name != "plan-smarter" {
		t.Errorf("active=%q want plan-smarter", name)
	}

	// Unknown profile returns 404.
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/profiles/activate/nope", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown activate status=%d want 404", w.Code)
	}

	// Empty name clears the active profile.
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/profiles/activate/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("clear status=%d", w.Code)
	}
	if name, _ := s.ActiveProfile(); name != "" {
		t.Errorf("active=%q want empty", name)
	}
}

func TestServer_ProfileAffectsModelListing(t *testing.T) {
	s := newProfileServer(t, newEchoRouter("glm-5.1", "qwen-3", "image-model"))

	listing := func() map[string]bool {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("/v1/models status=%d", w.Code)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		ids := make(map[string]bool, len(resp.Data))
		for _, d := range resp.Data {
			ids[d.ID] = true
		}
		return ids
	}

	// Without a profile, llm-plan lists under glm-5.1.
	ids := listing()
	if !ids["llm-plan"] {
		t.Errorf("expected llm-plan in default listing: %v", ids)
	}

	// With the profile, llm-plan resolves to qwen-3 so it no longer lists as a
	// glm-5.1 alias, and image-gen is disabled.
	if err := s.SetActiveProfile("plan-smarter"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	ids = listing()
	if ids["image-gen"] {
		t.Errorf("disabled alias image-gen should not be listed: %v", ids)
	}
}

// TestServer_ProfileParityGroupsAndMatrix asserts the profile overlay rewrites
// the requested model to the same target whether the backend is groups or
// matrix, confirming it is a routing-agnostic front layer.
func TestServer_ProfileParityGroupsAndMatrix(t *testing.T) {
	cases := []struct {
		name   string
		config string
	}{
		{"groups", profileServerConfig},
		{"matrix", profileServerMatrixConfig},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newProfileServerFrom(t, tc.config, newEchoRouter("glm-5.1", "qwen-3", "image-model"))

			// Default resolution.
			w := httptest.NewRecorder()
			s.ServeHTTP(w, chatRequest("llm-plan"))
			if w.Code != http.StatusOK || w.Body.String() != "glm-5.1" {
				t.Fatalf("%s default: status=%d body=%q want glm-5.1", tc.name, w.Code, w.Body.String())
			}

			// Overlay rewrites to qwen-3.
			if err := s.SetActiveProfile("plan-smarter"); err != nil {
				t.Fatalf("activate: %v", err)
			}
			w = httptest.NewRecorder()
			s.ServeHTTP(w, chatRequest("llm-plan"))
			if w.Code != http.StatusOK || w.Body.String() != "qwen-3" {
				t.Fatalf("%s profile: status=%d body=%q want qwen-3", tc.name, w.Code, w.Body.String())
			}

			// Disabled alias is not found.
			w = httptest.NewRecorder()
			s.ServeHTTP(w, chatRequest("image-gen"))
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s disabled: status=%d want 404", tc.name, w.Code)
			}
		})
	}
}
