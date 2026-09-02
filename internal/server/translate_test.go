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
	"github.com/tidwall/gjson"
)

// scriptedRouter is a LocalRouter whose ServeHTTP runs a test-supplied handler,
// standing in for a model's upstream process so the translation layer can be
// exercised end-to-end through the real dispatch pipeline.
type scriptedRouter struct {
	handled map[string]bool
	handler http.HandlerFunc
	running map[string]process.ProcessState
}

func (s *scriptedRouter) Handles(model string) bool                        { return s.handled[model] }
func (s *scriptedRouter) Shutdown(time.Duration) error                     { return nil }
func (s *scriptedRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler(w, r) }
func (s *scriptedRouter) RunningModels() map[string]process.ProcessState   { return s.running }
func (s *scriptedRouter) Unload(time.Duration, ...string)                  {}
func (s *scriptedRouter) ProcessLogger(string) (*logmon.Monitor, bool) {
	return nil, false
}

func newTranslateServer(t *testing.T, cfg config.Config, local router.LocalRouter) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	proxylog := logmon.NewWriter(io.Discard)
	s := &Server{
		cfg:         cfg,
		muxlog:      logmon.NewWriter(io.Discard),
		proxylog:    proxylog,
		upstreamlog: logmon.NewWriter(io.Discard),
		inflight:    &inflightCounter{},
		metrics:     newMetricsMonitor(proxylog, 0, 0, "", 0),
		local:       local,
		peer:        newStubRouter(nil, ""),
		shutdownCtx: ctx,
		shutdownFn:  cancel,
	}
	s.routes()
	return s
}

func oneModelConfig() config.Config {
	return config.Config{Models: map[string]config.ModelConfig{"test-model": {}}}
}

// openAIChatJSON is a minimal non-streaming OpenAI chat completion.
const openAIChatJSON = `{"id":"chatcmpl-x","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`

func TestServer_AnthropicMessages_BufferedTranslation(t *testing.T) {
	var gotPath string
	var gotBody []byte
	local := &scriptedRouter{
		handled: map[string]bool{"test-model": true},
		handler: func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(openAIChatJSON))
		},
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
	local := &scriptedRouter{
		handled: map[string]bool{"test-model": true},
		handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(sse))
		},
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

func TestServer_OllamaChat_BufferedTranslation(t *testing.T) {
	var gotPath string
	local := &scriptedRouter{
		handled: map[string]bool{"test-model": true},
		handler: func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(openAIChatJSON))
		},
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

// TestServer_OllamaHeadProbe verifies that HEAD / returns 200 (Ollama clients
// such as Enchanted probe it for reachability) while GET / still redirects to
// the UI.
func TestServer_OllamaHeadProbe(t *testing.T) {
	s := newTranslateServer(t, oneModelConfig(), &scriptedRouter{})

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
	s := newTranslateServer(t, cfg, &scriptedRouter{})

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
