package docbot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

	// No line of the prompt may name a tool the handler does not serve,
	// including the rule that says to check the code.
	prompt := bot.SystemPrompt()
	if !strings.Contains(prompt, "- `docs__search_docs`") {
		t.Error("prompt lost the docs__search_docs line")
	}
	for _, m := range toolRef.FindAllStringSubmatch(prompt, -1) {
		if !strings.HasPrefix(m[1], "docs__") {
			t.Errorf("docs-only prompt names %s", m[1])
		}
	}
	assertRulesNumbered(t, prompt)

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
	prompt := bot.SystemPrompt()
	if !strings.Contains(prompt, "- `code__grep`") || !strings.Contains(prompt, "search the source with `code__grep`") {
		t.Error("prompt dropped a code__grep line although it is served")
	}
	for _, gone := range []string{"config__get_config", "sys__now"} {
		if strings.Contains(prompt, gone) {
			t.Errorf("prompt still names %s", gone)
		}
	}
	assertRulesNumbered(t, prompt)

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
	prompt := "## Tools\n\n- `docs__a` - one.\n- `config__b` - two.\n\n" +
		"## Rules\n\n1. Call `docs__a` first.\n2. Then call `config__b`.\n3. Be brief.\n" +
		"Text that mentions `config__b` outside a list stays."
	got := filterPrompt(prompt, []string{"docs__a"})
	want := "## Tools\n\n- `docs__a` - one.\n\n" +
		"## Rules\n\n1. Call `docs__a` first.\n2. Be brief.\n" +
		"Text that mentions `config__b` outside a list stays."
	if got != want {
		t.Errorf("filterPrompt =\n%s\nwant\n%s", got, want)
	}

	if got := filterPrompt(prompt, []string{"docs__a", "config__b"}); got != prompt {
		t.Errorf("filterPrompt changed a prompt whose tools are all served:\n%s", got)
	}
}

// assertRulesNumbered checks the numbered rules run 1, 2, 3, ...
func assertRulesNumbered(t *testing.T, prompt string) {
	t.Helper()
	want := 1
	for _, line := range strings.Split(prompt, "\n") {
		if m := listItem.FindStringSubmatch(line); m != nil && m[1] != "" {
			if m[1] != strconv.Itoa(want) {
				t.Errorf("rule numbered %s, want %d: %.60s", m[1], want, line)
			}
			want++
		}
	}
	if want == 1 {
		t.Error("prompt has no numbered rules")
	}
}
