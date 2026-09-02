package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/tidwall/gjson"
)

// newTranslateServer builds a Server with a caller-supplied config and local
// router, so the Anthropic/Ollama translation layer can be exercised end-to-end
// through the real dispatch pipeline. It mirrors newTestServer but takes a cfg
// (the translation handlers resolve models via cfg.RealModelName).
func newTranslateServer(t *testing.T, cfg config.Config, local *stubRouter) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	proxylog := logmon.NewWriter(io.Discard)
	st, err := store.New("")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := &Server{
		cfg:         cfg,
		muxlog:      logmon.NewWriter(io.Discard),
		proxylog:    proxylog,
		upstreamlog: logmon.NewWriter(io.Discard),
		inflight:    newInflightTracker(),
		metrics:     newMetricsMonitor(proxylog, 0, 0, st),
		store:       st,
		local:       local,
		peer:        newStubRouter(nil, ""),
		shutdownCtx: ctx,
		shutdownFn:  cancel,
	}
	s.routes()
	return s
}

// oneModelConfig is a minimal config with a single local model.
func oneModelConfig() config.Config {
	return config.Config{Models: map[string]config.ModelConfig{"test-model": {}}}
}

// openAIChatJSON is a minimal non-streaming OpenAI chat completion.
const openAIChatJSON = `{"id":"chatcmpl-x","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`

func TestServer_AnthropicMessages_BufferedTranslation(t *testing.T) {
	var gotPath string
	var gotBody []byte
	local := newStubRouter([]string{"test-model"}, "")
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(openAIChatJSON))
	}
	s := newTranslateServer(t, oneModelConfig(), local)

	body := `{"model":"test-model","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	// The upstream saw an OpenAI-shaped request at the chat-completions path.
	if gotPath != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", gotPath)
	}
	if !gjson.GetBytes(gotBody, "messages").Exists() {
		t.Errorf("upstream body not OpenAI-shaped: %s", gotBody)
	}
	// The client got an Anthropic-shaped response.
	got := gjson.ParseBytes(w.Body.Bytes())
	if got.Get("type").String() != "message" {
		t.Errorf("response type = %q, want message; body=%s", got.Get("type").String(), w.Body.String())
	}
	if got.Get("role").String() != "assistant" {
		t.Errorf("response role = %q", got.Get("role").String())
	}
	if txt := got.Get("content.0.text").String(); txt != "hi" {
		t.Errorf("response text = %q, want hi", txt)
	}
}

func TestServer_AnthropicMessages_StreamingTranslation(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl-x","choices":[{"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	local := newStubRouter([]string{"test-model"}, "")
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sse)) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter
	}
	s := newTranslateServer(t, oneModelConfig(), local)

	body := `{"model":"test-model","stream":true,"max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	out := w.Body.String()
	for _, want := range []string{"event: message_start", "event: content_block_delta", "event: message_stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("streamed output missing %q:\n%s", want, out)
		}
	}
}

// TestServer_AnthropicMessages_Passthrough verifies a model with
// passthroughAnthropic set forwards the raw Anthropic body unchanged (no
// translation to the chat-completions path).
func TestServer_AnthropicMessages_Passthrough(t *testing.T) {
	var gotPath string
	var gotBody []byte
	local := newStubRouter([]string{"native"}, "")
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}
	cfg := config.Config{Models: map[string]config.ModelConfig{
		"native": {PassthroughAnthropic: true},
	}}
	s := newTranslateServer(t, cfg, local)

	body := `{"model":"native","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(httptest.NewRecorder(), req)

	if gotPath != "/v1/messages" {
		t.Errorf("upstream path = %q, want /v1/messages (unchanged)", gotPath)
	}
	if !gjson.GetBytes(gotBody, "max_tokens").Exists() {
		t.Errorf("passthrough body not Anthropic-shaped: %s", gotBody)
	}
}

func TestServer_OllamaChat_BufferedTranslation(t *testing.T) {
	var gotPath string
	local := newStubRouter([]string{"test-model"}, "")
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(openAIChatJSON))
	}
	s := newTranslateServer(t, oneModelConfig(), local)

	body := `{"model":"test-model","stream":false,"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", gotPath)
	}
	got := gjson.ParseBytes(w.Body.Bytes())
	if got.Get("message.content").String() != "hi" {
		t.Errorf("ollama content = %q, want hi; body=%s", got.Get("message.content").String(), w.Body.String())
	}
	if !got.Get("done").Bool() {
		t.Errorf("ollama done = false; body=%s", w.Body.String())
	}
}

// TestServer_OllamaHeadProbe verifies HEAD / returns 200 (Ollama clients such
// as Enchanted probe it for reachability) while GET / still redirects.
func TestServer_OllamaHeadProbe(t *testing.T) {
	s := newTranslateServer(t, oneModelConfig(), newStubRouter(nil, ""))

	wHead := httptest.NewRecorder()
	s.ServeHTTP(wHead, httptest.NewRequest(http.MethodHead, "/", nil))
	if wHead.Code != http.StatusOK {
		t.Errorf("HEAD / = %d, want 200", wHead.Code)
	}

	wGet := httptest.NewRecorder()
	s.ServeHTTP(wGet, httptest.NewRequest(http.MethodGet, "/", nil))
	if wGet.Code != http.StatusFound {
		t.Errorf("GET / = %d, want 302", wGet.Code)
	}
}

func TestServer_OllamaTags_ListsModels(t *testing.T) {
	cfg := config.Config{Models: map[string]config.ModelConfig{
		"alpha": {Name: "Alpha"},
		"beta":  {Unlisted: true},
	}}
	s := newTranslateServer(t, cfg, newStubRouter(nil, ""))

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Unlisted "beta" is excluded; only "alpha" is listed.
	if len(resp.Models) != 1 || resp.Models[0].Name != "alpha" {
		t.Errorf("tags = %+v, want only alpha", resp.Models)
	}
}

// TestServer_OllamaVersion_ServedByUpstream confirms /api/version is answered by
// the llama-swap handler (Ollama registration uses SkipVersion) and is disjoint
// from the llama-swap /api/* control endpoints.
func TestServer_OllamaVersion_ServedByUpstream(t *testing.T) {
	s := newTranslateServer(t, oneModelConfig(), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/api/version = %d, want 200", w.Code)
	}
	if !gjson.GetBytes(w.Body.Bytes(), "version").Exists() {
		t.Errorf("/api/version missing version field: %s", w.Body.String())
	}
}
