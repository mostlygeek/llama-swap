package ollama

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeDispatcher implements Dispatcher for handler tests. It records what was
// dispatched and lets the test control what the "upstream" writes.
type fakeDispatcher struct {
	dispatched []dispatchCall
	response   func(w http.ResponseWriter, r *http.Request, body []byte)
	models     []ModelInfo
	running    []string
}

type dispatchCall struct {
	Path string
	Body []byte
}

func (f *fakeDispatcher) DispatchJSON(w http.ResponseWriter, r *http.Request, body []byte) {
	f.dispatched = append(f.dispatched, dispatchCall{
		Path: r.URL.Path,
		Body: append([]byte(nil), body...),
	})
	if f.response != nil {
		f.response(w, r, body)
	}
}

func (f *fakeDispatcher) ListModels() []ModelInfo {
	return f.models
}

func (f *fakeDispatcher) FindModel(name string) (ModelInfo, bool) {
	for _, m := range f.models {
		if m.ID == name {
			return m, true
		}
		for _, a := range m.Aliases {
			if a == name {
				return m, true
			}
		}
	}
	return ModelInfo{}, false
}

func (f *fakeDispatcher) RunningModels() []string {
	return f.running
}

// newTestRouter mounts the Ollama routes on a plain ServeMux (no auth/inflight
// middleware needed in isolation).
func newTestRouter(d Dispatcher, opts Options) http.Handler {
	mux := http.NewServeMux()
	Register(func(method, path string, h http.HandlerFunc) {
		mux.Handle(method+" "+path, h)
	}, d, opts)
	return mux
}

func TestHandler_ChatNonStreaming_TranslatesShape(t *testing.T) {
	d := &fakeDispatcher{
		response: func(w http.ResponseWriter, r *http.Request, body []byte) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			openaiResp := `{
				"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":3,"completion_tokens":1}
			}`
			_, _ = w.Write([]byte(openaiResp))
		},
	}
	router := newTestRouter(d, Options{})

	reqBody := `{"model":"qwen3:8b","messages":[{"role":"user","content":"hello"}],"stream":false}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Errorf("Content-Type: got %q", got)
	}

	var resp ChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response JSON: %v\n%s", err, w.Body.String())
	}
	if resp.Model != "qwen3:8b" {
		t.Errorf("model: got %s", resp.Model)
	}
	if !resp.Done {
		t.Errorf("done: false")
	}
	if resp.Message.Content != "hi" {
		t.Errorf("content: got %s", resp.Message.Content)
	}
	if resp.PromptEvalCount != 3 || resp.EvalCount != 1 {
		t.Errorf("token counts: prompt=%d eval=%d", resp.PromptEvalCount, resp.EvalCount)
	}

	// Verify the body that got dispatched was OpenAI-shaped and the path was rewritten.
	if len(d.dispatched) != 1 {
		t.Fatalf("dispatched %d calls, want 1", len(d.dispatched))
	}
	if d.dispatched[0].Path != "/v1/chat/completions" {
		t.Errorf("path: got %s", d.dispatched[0].Path)
	}
	var dispatched map[string]any
	_ = json.Unmarshal(d.dispatched[0].Body, &dispatched)
	if dispatched["model"] != "qwen3:8b" {
		t.Errorf("dispatched model: got %v", dispatched["model"])
	}
	if _, hasMessages := dispatched["messages"]; !hasMessages {
		t.Errorf("dispatched body missing 'messages'")
	}
}

func TestHandler_ChatStreaming_EmitsNDJSON(t *testing.T) {
	d := &fakeDispatcher{
		response: func(w http.ResponseWriter, r *http.Request, body []byte) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			sse := strings.Join([]string{
				`data: {"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`,
				``,
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
				``,
				`data: [DONE]`,
				``,
				``,
			}, "\n")
			_, _ = w.Write([]byte(sse)) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter
		},
	}
	router := newTestRouter(d, Options{})

	reqBody := `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Errorf("Content-Type: got %q", got)
	}
	lines := bytes.Split(bytes.TrimRight(w.Body.Bytes(), "\n"), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("ndjson lines: got %d\n%s", len(lines), w.Body.String())
	}
	var first ChatResponse
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("line 0: %v", err)
	}
	if first.Message.Content != "hi" || first.Done {
		t.Errorf("first frame: content=%q done=%v", first.Message.Content, first.Done)
	}
	var final ChatResponse
	if err := json.Unmarshal(lines[1], &final); err != nil {
		t.Fatalf("line 1: %v", err)
	}
	if !final.Done {
		t.Errorf("final frame: done=false")
	}

	// Streaming requests should set stream_options.include_usage so we can
	// populate token counts on the final NDJSON line.
	var sent map[string]any
	_ = json.Unmarshal(d.dispatched[0].Body, &sent)
	so, _ := sent["stream_options"].(map[string]any)
	if so["include_usage"] != true {
		t.Errorf("missing stream_options.include_usage")
	}
}

func TestHandler_ChatDefaultsToStreaming(t *testing.T) {
	// stream field omitted -> default true per Ollama docs.
	d := &fakeDispatcher{
		response: func(w http.ResponseWriter, r *http.Request, body []byte) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\n" + `data: [DONE]` + "\n\n"))
		},
	}
	router := newTestRouter(d, Options{})

	reqBody := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(reqBody))
	router.ServeHTTP(w, req)

	if got := w.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Errorf("default-stream Content-Type: got %q", got)
	}
}

func TestHandler_ChatErrorPassThrough(t *testing.T) {
	d := &fakeDispatcher{
		response: func(w http.ResponseWriter, r *http.Request, body []byte) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"model not configured"}`))
		},
	}
	router := newTestRouter(d, Options{})

	reqBody := `{"model":"missing","messages":[{"role":"user","content":"x"}],"stream":false}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(reqBody))
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "model not configured") {
		t.Errorf("error body lost: %s", w.Body.String())
	}
}

func TestHandler_GenerateNonStreaming(t *testing.T) {
	d := &fakeDispatcher{
		response: func(w http.ResponseWriter, r *http.Request, body []byte) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"choices":[{"message":{"role":"assistant","content":"42"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1}
			}`))
		},
	}
	router := newTestRouter(d, Options{})

	reqBody := `{"model":"m","prompt":"what is 6*7","system":"be terse","stream":false}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/generate", strings.NewReader(reqBody))
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	var resp GenerateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if resp.Response != "42" {
		t.Errorf("response: got %s", resp.Response)
	}
	if !resp.Done {
		t.Errorf("done=false")
	}

	// Verify dispatched body used messages + system role.
	var dispatched map[string]any
	_ = json.Unmarshal(d.dispatched[0].Body, &dispatched)
	msgs := dispatched["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("dispatched messages: got %d", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("first dispatched message should be system")
	}
}

func TestHandler_EmbedTranslatesShape(t *testing.T) {
	d := &fakeDispatcher{
		response: func(w http.ResponseWriter, r *http.Request, body []byte) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]},{"embedding":[0.3,0.4]}],"usage":{"prompt_tokens":2}}`))
		},
	}
	router := newTestRouter(d, Options{})

	reqBody := `{"model":"e","input":["a","b"]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/embed", strings.NewReader(reqBody))
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}
	var resp EmbedResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if len(resp.Embeddings) != 2 {
		t.Errorf("embeddings count: got %d", len(resp.Embeddings))
	}
	if d.dispatched[0].Path != "/v1/embeddings" {
		t.Errorf("path: got %s", d.dispatched[0].Path)
	}
}

func TestHandler_EmbeddingsLegacyShape(t *testing.T) {
	d := &fakeDispatcher{
		response: func(w http.ResponseWriter, r *http.Request, body []byte) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"embedding":[1.0,2.0,3.0]}]}`))
		},
	}
	router := newTestRouter(d, Options{})

	reqBody := `{"model":"e","prompt":"hello"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/embeddings", strings.NewReader(reqBody))
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}
	var resp EmbeddingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if len(resp.Embedding) != 3 {
		t.Errorf("embedding length: got %d", len(resp.Embedding))
	}

	// Legacy endpoint should send "input" string, not "prompt".
	var dispatched map[string]any
	_ = json.Unmarshal(d.dispatched[0].Body, &dispatched)
	if dispatched["input"] != "hello" {
		t.Errorf("dispatched input: got %v", dispatched["input"])
	}
}

func TestHandler_TagsListsAllModels(t *testing.T) {
	d := &fakeDispatcher{
		models: []ModelInfo{
			{ID: "qwen3:8b"},
			{ID: "llama3.2:latest"},
		},
	}
	router := newTestRouter(d, Options{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/tags", nil))

	var resp TagsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("models count: got %d", len(resp.Models))
	}
	// Sorted by name
	if resp.Models[0].Name != "llama3.2:latest" {
		t.Errorf("first model: got %s", resp.Models[0].Name)
	}
}

func TestHandler_ShowReturnsModelInfo(t *testing.T) {
	d := &fakeDispatcher{
		models: []ModelInfo{
			{
				ID:       "qwen3:8b",
				Metadata: map[string]any{"tools": true, "vision": true},
			},
		},
	}
	router := newTestRouter(d, Options{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/show", strings.NewReader(`{"model":"qwen3:8b"}`)))

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}
	var resp ShowResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if !strings.Contains(resp.Modelfile, "qwen3:8b") {
		t.Errorf("modelfile: got %s", resp.Modelfile)
	}
	hasTools := false
	hasVision := false
	for _, cap := range resp.Capabilities {
		if cap == "tools" {
			hasTools = true
		}
		if cap == "vision" {
			hasVision = true
		}
	}
	if !hasTools || !hasVision {
		t.Errorf("capabilities: got %v, want tools+vision", resp.Capabilities)
	}
}

func TestHandler_ShowUnknownModel(t *testing.T) {
	d := &fakeDispatcher{models: []ModelInfo{{ID: "known"}}}
	router := newTestRouter(d, Options{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/show", strings.NewReader(`{"model":"unknown"}`)))
	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", w.Code)
	}
}

func TestHandler_PsRunningModels(t *testing.T) {
	d := &fakeDispatcher{
		models:  []ModelInfo{{ID: "a"}, {ID: "b"}},
		running: []string{"b"},
	}
	router := newTestRouter(d, Options{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/ps", nil))

	var resp PsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].Name != "b" {
		t.Errorf("ps models: got %v", resp.Models)
	}
}

func TestHandler_Version(t *testing.T) {
	router := newTestRouter(&fakeDispatcher{}, Options{Version: "0.5.0-test"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/version", nil))
	var resp VersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if resp.Version != "0.5.0-test" {
		t.Errorf("version: got %s", resp.Version)
	}
}

func TestHandler_ManagementEndpoints_Return501(t *testing.T) {
	router := newTestRouter(&fakeDispatcher{}, Options{})
	cases := []struct {
		method, path string
	}{
		{"POST", "/api/create"},
		{"POST", "/api/copy"},
		{"DELETE", "/api/delete"},
		{"POST", "/api/pull"},
		{"POST", "/api/push"},
		{"HEAD", "/api/blobs/abc"},
		{"POST", "/api/blobs/abc"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: status %d want 501", tc.method, tc.path, w.Code)
		}
	}
}

func TestHandler_MissingModelField_400(t *testing.T) {
	router := newTestRouter(&fakeDispatcher{}, Options{})
	cases := []struct {
		path, body string
	}{
		{"/api/chat", `{"messages":[]}`},
		{"/api/generate", `{"prompt":"x"}`},
		{"/api/embed", `{"input":"x"}`},
		{"/api/embeddings", `{"prompt":"x"}`},
		{"/api/show", `{}`},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d want 400, body=%s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestHandler_PassthroughOllama_ForwardsRaw(t *testing.T) {
	d := &fakeDispatcher{
		models: []ModelInfo{{ID: "native", PassthroughOllama: true}},
		response: func(w http.ResponseWriter, r *http.Request, body []byte) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			// A real Ollama backend's native response, returned unchanged.
			_, _ = w.Write([]byte(`{"model":"native","message":{"role":"assistant","content":"hi"},"done":true}`))
		},
	}
	router := newTestRouter(d, Options{})

	reqBody := `{"model":"native","messages":[{"role":"user","content":"hello"}],"stream":false}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	if len(d.dispatched) != 1 {
		t.Fatalf("dispatched %d calls, want 1", len(d.dispatched))
	}
	// Path is left as /api/chat (not rewritten) and the body is forwarded raw.
	if d.dispatched[0].Path != "/api/chat" {
		t.Errorf("path: got %s, want /api/chat", d.dispatched[0].Path)
	}
	if string(d.dispatched[0].Body) != reqBody {
		t.Errorf("body was translated; got %s", string(d.dispatched[0].Body))
	}
	// Response flowed straight back, untranslated.
	if !strings.Contains(w.Body.String(), `"done":true`) {
		t.Errorf("unexpected response body: %s", w.Body.String())
	}
}
