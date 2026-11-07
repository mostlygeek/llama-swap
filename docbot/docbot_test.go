package docbot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mostlygeek/llama-swap/internal/mcpserver"
	"github.com/mostlygeek/llama-swap/internal/mcptools/sourcecode"
)

// call posts one JSON-RPC request with the MCP headers and decodes the
// result into out.
func call(t *testing.T, h http.Handler, method string, params map[string]any, out any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", bytes.NewReader(body))
	req.Header.Set(mcpserver.HeaderProtocolVersion, mcpserver.ProtocolVersion)
	req.Header.Set(mcpserver.HeaderMethod, method)
	if name, ok := params["name"].(string); ok {
		req.Header.Set(mcpserver.HeaderName, name)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var reply struct {
		Result json.RawMessage     `json:"result"`
		Error  *mcpserver.RPCError `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
	}
	if reply.Error != nil {
		t.Fatalf("%s: %s", method, reply.Error.Message)
	}
	if err := json.Unmarshal(reply.Result, out); err != nil {
		t.Fatal(err)
	}
}

func TestDocbot_DocsOnly(t *testing.T) {
	bot, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	want := "docs__get_config_schema docs__get_doc docs__list_docs docs__search_docs"
	if got := strings.Join(bot.Tools(), " "); got != want {
		t.Errorf("Tools() = %s, want %s", got, want)
	}

	prompt := bot.SystemPrompt()
	if !strings.Contains(prompt, "- `docs__search_docs`") {
		t.Error("prompt lost the docs__search_docs line")
	}
	for _, gone := range []string{"- `config__get_config`", "- `sys__now`", "- `code__grep`"} {
		if strings.Contains(prompt, gone) {
			t.Errorf("prompt still lists %s", gone)
		}
	}

	var result mcpserver.CallToolResult
	call(t, bot, "tools/call", map[string]any{"name": "docs__search_docs", "arguments": map[string]any{"query": "ttl"}}, &result)
	if result.IsError || !strings.Contains(result.Content[0].Text, "ttl") {
		t.Errorf("search: %+v", result)
	}
}

func TestDocbot_WithSource(t *testing.T) {
	src := fstest.MapFS{"internal/a/a.go": {Data: []byte("package a\n\nfunc Hello() {}\n")}}
	bot, err := New(Options{Name: "test", Source: src, SourceRef: "abc123"})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(strings.Join(bot.Tools(), " "), "code__grep") {
		t.Errorf("Tools() = %v, want code__* tools", bot.Tools())
	}
	if !strings.Contains(bot.SystemPrompt(), "- `code__grep`") {
		t.Error("prompt dropped code__grep although it is served")
	}

	var result mcpserver.CallToolResult
	call(t, bot, "tools/call", map[string]any{"name": "code__read", "arguments": map[string]any{"path": "internal/a/a.go"}}, &result)
	text := result.Content[0].Text
	if result.IsError || !strings.Contains(text, "func Hello") || !strings.Contains(text, "/blob/abc123/internal/a/a.go") {
		t.Errorf("code__read: %s", text)
	}

	var discover struct {
		Meta map[string]struct {
			Name string `json:"name"`
		} `json:"_meta"`
	}
	call(t, bot, "server/discover", nil, &discover)
	if got := discover.Meta["io.modelcontextprotocol/serverInfo"].Name; got != "test" {
		t.Errorf("serverInfo.name = %q", got)
	}
}

func TestDocbot_RejectsGET(t *testing.T) {
	bot, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	bot.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", w.Code)
	}
}

func TestDocbot_SourceFromArchive(t *testing.T) {
	var buf bytes.Buffer
	if err := sourcecode.WriteArchive(&buf, fstest.MapFS{"main.go": {Data: []byte("package main\n")}}); err != nil {
		t.Fatal(err)
	}
	src, err := SourceFromArchive(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Open("main.go"); err != nil {
		t.Error(err)
	}
}

func TestDocbot_PseudoVersionRef(t *testing.T) {
	m := pseudoRev.FindStringSubmatch("v0.0.0-20261106234400-e77fe59e642b")
	if m == nil || m[1] != "e77fe59e642b" {
		t.Errorf("pseudoRev = %v", m)
	}
	// Inside llama-swap's own tests there is no dependency to read.
	if got := buildRef(); got != sourcecode.DefaultRef {
		t.Errorf("buildRef() = %q", got)
	}
}

func TestDocbot_FilterPrompt(t *testing.T) {
	prompt := "## Tools\n\n- `docs__a` - one.\n- `config__b` - two.\n\nRule: never call `config__b`."
	got := filterPrompt(prompt, []string{"docs__a"})
	if want := "## Tools\n\n- `docs__a` - one.\n\nRule: never call `config__b`."; got != want {
		t.Errorf("filterPrompt =\n%s\nwant\n%s", got, want)
	}
}
