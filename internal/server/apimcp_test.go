package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/docagent"
	"github.com/mostlygeek/llama-swap/internal/mcptools"
)

// mcpTestFS is a small stand-in for the repository layout.
func mcpTestFS() fstest.MapFS {
	return fstest.MapFS{
		"config.example.yaml": &fstest.MapFile{Data: []byte(`# header

# globalTTL: the default TTL in seconds before unloading a model
# - optional, default: 0
globalTTL: 0

# models: a dictionary of model configurations
# - required
models:
  example:
    cmd: llama-server --port ${PORT}
`)},
		"config-schema.json": &fstest.MapFile{Data: []byte(`{
  "type": "object",
  "required": ["models"],
  "properties": {
    "globalTTL": {"type": "integer", "description": "Default TTL in seconds.", "default": 0},
    "models": {
      "type": "object",
      "additionalProperties": {
        "type": "object",
        "required": ["cmd"],
        "properties": {"cmd": {"type": "string", "description": "The command to run."}}
      }
    }
  }
}`)},
		"internal/docagent/db/guides/routing/unloading.md": &fstest.MapFile{Data: []byte(`---
title: Unloading models
summary: How ttl frees VRAM.
category: guides
tags: [ttl, vram]
config_keys: [globalTTL]
---

# Unloading models

Set ttl to unload an idle model and free its VRAM.
`)},
		"internal/docagent/db/tutorials/start.md": &fstest.MapFile{Data: []byte(`---
title: Getting started
summary: Build a first config.
category: tutorials
---

# Getting started

Write a models block.
`)},
	}
}

func newMCPServer() *Server {
	return newTestServerWithReference(newStubRouter(nil, ""), newStubRouter(nil, ""), mcpTestFS())
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// mcpRequest posts a JSON-RPC body with the standard MCP headers. A blank
// version, method or name header is omitted so tests can exercise the
// missing-header paths; "-" forces an explicitly wrong value.
func mcpRequest(body, version, method, name string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if version != "" {
		req.Header.Set(headerMCPProtocolVersion, version)
	}
	if method != "" {
		req.Header.Set(headerMCPMethod, method)
	}
	if name != "" {
		req.Header.Set(headerMCPName, name)
	}
	return req
}

// rpc issues a well-formed request and decodes the envelope.
func rpc(t *testing.T, s *Server, method, name, params string) (*httptest.ResponseRecorder, rpcEnvelope) {
	t.Helper()

	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `"`
	if params != "" {
		body += `,"params":` + params
	}
	body += `}`

	w := httptest.NewRecorder()
	s.ServeHTTP(w, mcpRequest(body, mcpProtocolVersion, method, name))

	var env rpcEnvelope
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decoding %s response: %v (body=%q)", method, err, w.Body.String())
		}
	}
	return w, env
}

// callTool issues a tools/call and returns the decoded CallToolResult.
func callToolRPC(t *testing.T, s *Server, name, args string) callToolResult {
	t.Helper()

	params := `{"name":"` + name + `"`
	if args != "" {
		params += `,"arguments":` + args
	}
	params += `}`

	w, env := rpc(t, s, "tools/call", name, params)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	if env.Error != nil {
		t.Fatalf("tools/call returned a JSON-RPC error: %+v", env.Error)
	}

	var result callToolResult
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decoding CallToolResult: %v (result=%s)", err, env.Result)
	}
	return result
}

// text joins a result's text blocks the way a client would.
func (r callToolResult) text() string {
	var parts []string
	for _, block := range r.Content {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ---------------------------------------------------------------- transport

func TestServer_APIMCP_RejectsNonPost(t *testing.T) {
	s := newMCPServer()
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(method, "/api/mcp", nil))

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, w.Code)
		}
		if allow := w.Header().Get("Allow"); allow != "POST" {
			t.Errorf("%s Allow = %q, want POST", method, allow)
		}
	}
}

func TestServer_APIMCP_DisabledWithoutReference(t *testing.T) {
	// newTestServer leaves s.reference nil, which is every pre-existing test.
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, mcpProtocolVersion, "tools/list", ""))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var got map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["enabled"] {
		t.Error("enabled = true, want false")
	}
}

func TestServer_APIMCP_RejectsOversizedBody(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + strings.Repeat("x", maxMCPBodyBytes) + `"}}`

	w := httptest.NewRecorder()
	newMCPServer().ServeHTTP(w, mcpRequest(body, mcpProtocolVersion, "tools/call", "x"))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

// A client on an older revision opens with `initialize`. Telling it which
// version we speak is far more useful than "method not found".
func TestServer_APIMCP_RejectsUnsupportedProtocolVersion(t *testing.T) {
	s := newMCPServer()

	tests := []struct {
		name    string
		version string
		method  string
		body    string
	}{
		{"missing version header", "", "tools/list", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
		{"older revision", "2025-06-18", "tools/list", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
		{"newer revision", "2027-01-01", "tools/list", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
		{"legacy initialize", "2025-11-25", "initialize", `{"jsonrpc":"2.0","id":0,"method":"initialize"}`},
		{"initialize with no headers at all", "", "", `{"jsonrpc":"2.0","id":0,"method":"initialize"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, mcpRequest(tt.body, tt.version, tt.method, ""))

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (JSON-RPC errors ride on 200)", w.Code)
			}

			var env rpcEnvelope
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if env.Error == nil {
				t.Fatalf("want an error, got result %s", env.Result)
			}
			if env.Error.Code != codeUnsupportedProtocolVersion {
				t.Errorf("code = %d, want %d", env.Error.Code, codeUnsupportedProtocolVersion)
			}

			var data struct {
				Supported []string `json:"supported"`
				Requested string   `json:"requested"`
			}
			if err := json.Unmarshal(env.Error.Data, &data); err != nil {
				t.Fatalf("decoding error data: %v", err)
			}
			if len(data.Supported) != 1 || data.Supported[0] != mcpProtocolVersion {
				t.Errorf("supported = %v, want [%s]", data.Supported, mcpProtocolVersion)
			}
			if data.Requested != tt.version {
				t.Errorf("requested = %q, want %q", data.Requested, tt.version)
			}
		})
	}
}

func TestServer_APIMCP_ValidatesStandardHeaders(t *testing.T) {
	s := newMCPServer()

	tests := []struct {
		name    string
		body    string
		method  string
		nameHdr string
		wantHas string
	}{
		{
			name:    "missing Mcp-Method",
			body:    `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			wantHas: "missing required Mcp-Method",
		},
		{
			name:    "Mcp-Method disagrees with the body",
			body:    `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			method:  "tools/call",
			wantHas: "header mismatch",
		},
		{
			name:    "missing Mcp-Name on tools/call",
			body:    `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"docs__list_docs"}}`,
			method:  "tools/call",
			wantHas: "missing required Mcp-Name",
		},
		{
			name:    "Mcp-Name disagrees with params.name",
			body:    `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"docs__list_docs"}}`,
			method:  "tools/call",
			nameHdr: "docs__get_doc",
			wantHas: "header mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, mcpRequest(tt.body, mcpProtocolVersion, tt.method, tt.nameHdr))

			var env rpcEnvelope
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v (body=%q)", err, w.Body.String())
			}
			if env.Error == nil {
				t.Fatalf("want an error, got result %s", env.Result)
			}
			if env.Error.Code != codeInvalidRequest {
				t.Errorf("code = %d, want %d", env.Error.Code, codeInvalidRequest)
			}
			if !strings.Contains(env.Error.Message, tt.wantHas) {
				t.Errorf("message = %q, want it to contain %q", env.Error.Message, tt.wantHas)
			}
		})
	}
}

// JSON-RPC batching was removed from MCP in revision 2025-06-18.
func TestServer_APIMCP_RejectsBatch(t *testing.T) {
	body := `[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`

	w := httptest.NewRecorder()
	newMCPServer().ServeHTTP(w, mcpRequest(body, mcpProtocolVersion, "tools/list", ""))

	var env rpcEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error == nil || env.Error.Code != codeInvalidRequest {
		t.Fatalf("error = %+v, want code %d", env.Error, codeInvalidRequest)
	}
}

func TestServer_APIMCP_MalformedBody(t *testing.T) {
	s := newMCPServer()
	for _, body := range []string{"{not json", `"a string"`, "42", "", `{"jsonrpc":"2.0","id":1}`} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, mcpRequest(body, mcpProtocolVersion, "tools/list", ""))

		var env rpcEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("body %q: decode: %v", body, err)
		}
		if env.Error == nil || env.Error.Code != codeParseError {
			t.Errorf("body %q: error = %+v, want code %d", body, env.Error, codeParseError)
		}
	}
}

func TestServer_APIMCP_UnknownMethod(t *testing.T) {
	_, env := rpc(t, newMCPServer(), "resources/list", "", "")

	if env.Error == nil || env.Error.Code != codeMethodNotFound {
		t.Fatalf("error = %+v, want code %d", env.Error, codeMethodNotFound)
	}
}

func TestServer_APIMCP_NotificationReturns202(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"notifications/cancelled"}`

	w := httptest.NewRecorder()
	newMCPServer().ServeHTTP(w, mcpRequest(body, mcpProtocolVersion, "notifications/cancelled", ""))

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", w.Body.String())
	}
}

// server/discover is how a 2026-07-28 client opens a connection: it asks what
// the server supports rather than performing the old initialize handshake, and
// only falls back to initialize if discover fails. Without it a conforming
// client cannot connect at all.
func TestServer_APIMCP_Discover(t *testing.T) {
	w, env := rpc(t, newMCPServer(), "server/discover", "", "")

	if w.Code != http.StatusOK || env.Error != nil {
		t.Fatalf("status = %d, error = %+v", w.Code, env.Error)
	}

	var result struct {
		SupportedVersions []string `json:"supportedVersions"`
		Capabilities      struct {
			Tools *struct{} `json:"tools"`
		} `json:"capabilities"`
		Instructions string `json:"instructions"`
		CacheScope   string `json:"cacheScope"`
		Meta         struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"io.modelcontextprotocol/serverInfo"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode: %v (result=%s)", err, env.Result)
	}

	if len(result.SupportedVersions) != 1 || result.SupportedVersions[0] != mcpProtocolVersion {
		t.Errorf("supportedVersions = %v, want [%s]", result.SupportedVersions, mcpProtocolVersion)
	}
	if result.Capabilities.Tools == nil {
		t.Error("capabilities.tools is absent, so a client will not call tools/list")
	}
	if result.Instructions == "" {
		t.Error("instructions is empty")
	}
	if result.Meta.ServerInfo.Name != "llama-swap" {
		t.Errorf("serverInfo.name = %q, want llama-swap", result.Meta.ServerInfo.Name)
	}
	if result.CacheScope != "public" {
		t.Errorf("cacheScope = %q, want public", result.CacheScope)
	}
}

func TestServer_APIMCP_Ping(t *testing.T) {
	w, env := rpc(t, newMCPServer(), "ping", "", "")

	if w.Code != http.StatusOK || env.Error != nil {
		t.Fatalf("status = %d, error = %+v", w.Code, env.Error)
	}
	if string(env.Result) != "{}" {
		t.Errorf("result = %s, want {}", env.Result)
	}
}

func TestServer_APIMCP_EchoesProtocolVersionHeader(t *testing.T) {
	w, _ := rpc(t, newMCPServer(), "tools/list", "", "")

	if got := w.Header().Get(headerMCPProtocolVersion); got != mcpProtocolVersion {
		t.Errorf("%s = %q, want %q", headerMCPProtocolVersion, got, mcpProtocolVersion)
	}
}

func TestServer_APIMCP_RequiresAPIKey(t *testing.T) {
	s := newMCPServer()
	s.cfg = config.Config{RequiredAPIKeys: []string{"sk-test"}}
	s.routes()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	t.Run("without a key", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, mcpRequest(body, mcpProtocolVersion, "tools/list", ""))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("with a key", func(t *testing.T) {
		req := mcpRequest(body, mcpProtocolVersion, "tools/list", "")
		req.Header.Set("Authorization", "Bearer sk-test")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})
}

// ------------------------------------------------------------------- tools

func TestServer_APIMCP_ToolsList(t *testing.T) {
	w, env := rpc(t, newMCPServer(), "tools/list", "", "")

	if w.Code != http.StatusOK || env.Error != nil {
		t.Fatalf("status = %d, error = %+v", w.Code, env.Error)
	}

	var result struct {
		Tools []mcptools.Tool `json:"tools"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	names := map[string]mcptools.Tool{}
	for _, tool := range result.Tools {
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has no inputSchema", tool.Name)
		}
		// Every tool here only reads embedded documentation.
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q is not marked readOnlyHint", tool.Name)
		}
		names[tool.Name] = tool
	}

	for _, want := range []string{"docs__list_docs", "docs__get_doc", "docs__search_docs", "docs__get_config_schema"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing tool %q", want)
		}
	}
}

// The id enum is what stops a small model inventing document ids.
func TestServer_APIMCP_GetDocIDIsEnumConstrained(t *testing.T) {
	_, env := rpc(t, newMCPServer(), "tools/list", "", "")

	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Properties struct {
					ID struct {
						Enum []string `json:"enum"`
					} `json:"id"`
				} `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, tool := range result.Tools {
		if tool.Name != "docs__get_doc" {
			continue
		}
		found := map[string]bool{}
		for _, id := range tool.InputSchema.Properties.ID.Enum {
			found[id] = true
		}
		for _, want := range []string{"guides/routing/unloading", "reference/config/models"} {
			if !found[want] {
				t.Errorf("enum missing %q, got %v", want, tool.InputSchema.Properties.ID.Enum)
			}
		}
		return
	}
	t.Fatal("docs__get_doc tool not advertised")
}

// The registry aggregates providers, so tools/list must carry both namespaces.
func TestServer_APIMCP_ToolsListSpansProviders(t *testing.T) {
	_, env := rpc(t, newMCPServer(), "tools/list", "", "")

	var result struct {
		Tools      []mcptools.Tool `json:"tools"`
		NextCursor string          `json:"nextCursor"`
		TTLMs      int             `json:"ttlMs"`
		CacheScope string          `json:"cacheScope"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	joined := strings.Join(names, ",")

	for _, want := range []string{"docs__get_doc", "sys__now", "config__get_config"} {
		if !strings.Contains(joined, want) {
			t.Errorf("tools/list missing %q, got %v", want, names)
		}
	}
	// Sorted by qualified name, which is what makes cursors meaningful.
	if !sort.StringsAreSorted(names) {
		t.Errorf("tools are not in stable sorted order: %v", names)
	}
	// A stateless server can never push notifications/tools/list_changed, so
	// the cache hint is how clients know when to re-poll.
	if result.TTLMs <= 0 {
		t.Errorf("ttlMs = %d, want a positive re-poll hint", result.TTLMs)
	}
	if result.CacheScope != "public" {
		t.Errorf("cacheScope = %q, want public", result.CacheScope)
	}
	if result.NextCursor != "" {
		t.Errorf("nextCursor = %q, want empty: everything fits one page", result.NextCursor)
	}
}

// Cursor plumbing at the transport level; the paging arithmetic itself is
// covered in internal/mcptools.
func TestServer_APIMCP_ToolsListRejectsBadCursor(t *testing.T) {
	w, env := rpc(t, newMCPServer(), "tools/list", "", `{"cursor":"!!not base64!!"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if env.Error == nil || env.Error.Code != codeInvalidParams {
		t.Fatalf("error = %+v, want code %d", env.Error, codeInvalidParams)
	}
}

// The sys namespace exists to prove aggregation, and is where the performance
// and hardware subsystems will surface later.
func TestServer_APIMCP_SysNow(t *testing.T) {
	result := callToolRPC(t, newMCPServer(), "sys__now", `{}`)

	if result.IsError {
		t.Fatalf("isError = true: %s", result.text())
	}
	if !strings.Contains(result.text(), testClock.Format("2006-01-02T15:04:05Z")) {
		t.Errorf("content does not reflect the injected clock:\n%s", result.text())
	}
}

func TestServer_APIMCP_SysNowUnknownZoneIsAToolError(t *testing.T) {
	result := callToolRPC(t, newMCPServer(), "sys__now", `{"timezone":"Mars/Olympus_Mons"}`)

	if !result.IsError {
		t.Error("isError = false, want true")
	}
	if !strings.Contains(result.text(), "unknown timezone") {
		t.Errorf("content = %q", result.text())
	}
}

// Routing is by namespace prefix, so a call must reach the right provider even
// when both are registered.
func TestServer_APIMCP_RoutesAcrossProviders(t *testing.T) {
	s := newMCPServer()

	docs := callToolRPC(t, s, "docs__get_doc", `{"id":"guides/routing/unloading"}`)
	if !strings.Contains(docs.text(), "free its VRAM") {
		t.Errorf("docs__get_doc did not reach the docs provider:\n%s", docs.text())
	}

	sys := callToolRPC(t, s, "sys__now", `{}`)
	if !strings.Contains(sys.text(), "unix:") {
		t.Errorf("sys__now did not reach the sys provider:\n%s", sys.text())
	}

	cfg := callToolRPC(t, s, "config__get_config", `{}`)
	if !strings.Contains(cfg.text(), "```yaml") {
		t.Errorf("config__get_config did not reach the config provider:\n%s", cfg.text())
	}

	// A local name that exists in one namespace must not be reachable in the
	// other.
	_, env := rpc(t, s, "tools/call", "sys__get_doc", `{"name":"sys__get_doc"}`)
	if env.Error == nil {
		t.Error("sys__get_doc resolved, want an unknown-tool error")
	}
}

func TestServer_APIMCP_UnknownToolIsAProtocolError(t *testing.T) {
	_, env := rpc(t, newMCPServer(), "tools/call", "nope", `{"name":"nope"}`)

	if env.Error == nil || env.Error.Code != codeInvalidRequest {
		t.Fatalf("error = %+v, want code %d", env.Error, codeInvalidRequest)
	}
}

// A tool that ran but could not answer must come back as a *result* with
// isError set, so the model can read the correction and retry. Only protocol
// failures become JSON-RPC errors.
func TestServer_APIMCP_ToolFailureIsIsErrorNotJSONRPC(t *testing.T) {
	s := newMCPServer()

	tests := []struct {
		name    string
		tool    string
		args    string
		wantHas string
	}{
		{"near miss suggests the real id", "docs__get_doc", `{"id":"guides/unload"}`, "guides/routing/unloading"},
		{"missing required argument", "docs__get_doc", `{}`, `"id" is required`},
		{"path traversal is just a miss", "docs__get_doc", `{"id":"../../etc/passwd"}`, "no document with id"},
		{"unknown schema path", "docs__get_config_schema", `{"path":"models.*.nope"}`, "no schema at path"},
		{"no documents matched", "docs__list_docs", `{"category":"nonsense"}`, "Valid categories"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callToolRPC(t, s, tt.tool, tt.args)
			if !result.IsError {
				t.Errorf("isError = false, want true")
			}
			if !strings.Contains(result.text(), tt.wantHas) {
				t.Errorf("content missing %q:\n%s", tt.wantHas, result.text())
			}
		})
	}
}

func TestServer_APIMCP_ListDocs(t *testing.T) {
	s := newMCPServer()

	t.Run("everything", func(t *testing.T) {
		result := callToolRPC(t, s, "docs__list_docs", `{}`)
		if result.IsError {
			t.Fatalf("isError = true: %s", result.text())
		}
		for _, want := range []string{
			"tutorials/start", "guides/routing/unloading",
			"reference/config/globalTTL", "reference/config/models",
			"How ttl frees VRAM.",
		} {
			if !strings.Contains(result.text(), want) {
				t.Errorf("content missing %q:\n%s", want, result.text())
			}
		}
		// Tutorials come before guides, which come before reference.
		if strings.Index(result.text(), "tutorials/start") > strings.Index(result.text(), "reference/config/models") {
			t.Errorf("categories out of order:\n%s", result.text())
		}
	})

	t.Run("no arguments at all", func(t *testing.T) {
		if callToolRPC(t, s, "docs__list_docs", "").IsError {
			t.Error("a tools/call with no arguments should still work")
		}
	})

	filters := []struct {
		name       string
		args       string
		wantHas    string
		wantHasNot string
	}{
		{"by category", `{"category":"guides"}`, "guides/routing/unloading", "tutorials/start"},
		{"by tag", `{"tag":"ttl"}`, "guides/routing/unloading", "tutorials/start"},
	}
	for _, tt := range filters {
		t.Run(tt.name, func(t *testing.T) {
			result := callToolRPC(t, s, "docs__list_docs", tt.args)
			if !strings.Contains(result.text(), tt.wantHas) {
				t.Errorf("content missing %q:\n%s", tt.wantHas, result.text())
			}
			if strings.Contains(result.text(), tt.wantHasNot) {
				t.Errorf("content should not contain %q:\n%s", tt.wantHasNot, result.text())
			}
		})
	}
}

func TestServer_APIMCP_GetDoc(t *testing.T) {
	s := newMCPServer()

	t.Run("knowledge base article", func(t *testing.T) {
		result := callToolRPC(t, s, "docs__get_doc", `{"id":"guides/routing/unloading"}`)
		if result.IsError {
			t.Fatalf("isError = true: %s", result.text())
		}
		if !strings.Contains(result.text(), "free its VRAM") {
			t.Errorf("body missing:\n%s", result.text())
		}
		if !strings.Contains(result.text(), "internal/docagent/db/guides/routing/unloading.md") {
			t.Errorf("citation missing:\n%s", result.text())
		}
		if strings.Contains(result.text(), "summary: How ttl") {
			t.Errorf("frontmatter leaked into the body:\n%s", result.text())
		}
	})

	t.Run("config section is fenced yaml with a line citation", func(t *testing.T) {
		result := callToolRPC(t, s, "docs__get_doc", `{"id":"reference/config/globalTTL"}`)
		if !strings.Contains(result.text(), "```yaml") {
			t.Errorf("not fenced as yaml:\n%s", result.text())
		}
		if !strings.Contains(result.text(), "globalTTL: 0") {
			t.Errorf("key line missing:\n%s", result.text())
		}
		if !strings.Contains(result.text(), "config.example.yaml lines 3-") {
			t.Errorf("line citation missing:\n%s", result.text())
		}
	})
}

func longDocServer(t *testing.T, lines int) *Server {
	t.Helper()

	var long strings.Builder
	long.WriteString("---\ntitle: Long\nsummary: Long doc.\ncategory: guides\n---\n\n")
	for i := 0; i < lines; i++ {
		long.WriteString("line of text\n")
	}

	fsys := mcpTestFS()
	fsys["internal/docagent/db/guides/routing/long.md"] = &fstest.MapFile{Data: []byte(long.String())}
	return newTestServerWithReference(newStubRouter(nil, ""), newStubRouter(nil, ""), fsys)
}

func TestServer_APIMCP_GetDocPaginates(t *testing.T) {
	s := longDocServer(t, 900)

	first := callToolRPC(t, s, "docs__get_doc", `{"id":"guides/routing/long"}`)
	if !strings.Contains(first.text(), "offset=") {
		t.Errorf("no continuation hint:\n%s", first.text())
	}
	if len(first.text()) > mcptools.MaxToolResultBytes+64 {
		t.Errorf("content is %d bytes, want <= %d", len(first.text()), mcptools.MaxToolResultBytes)
	}

	second := callToolRPC(t, s, "docs__get_doc", `{"id":"guides/routing/long","offset":250,"max_lines":10}`)
	if second.IsError {
		t.Fatalf("isError = true: %s", second.text())
	}
	if !strings.Contains(second.text(), "lines 257-") {
		t.Errorf("offset not reflected in the citation:\n%s", second.text())
	}
}

// max_lines above the hard cap must clamp rather than be honoured.
func TestServer_APIMCP_GetDocClampsMaxLines(t *testing.T) {
	result := callToolRPC(t, longDocServer(t, 900), "docs__get_doc", `{"id":"guides/routing/long","max_lines":5000}`)

	if lines := strings.Count(result.text(), "\n"); lines > docagent.MaxDocLines+20 {
		t.Errorf("returned %d lines, want <= %d", lines, docagent.MaxDocLines)
	}
}

func TestServer_APIMCP_SearchDocs(t *testing.T) {
	s := newMCPServer()

	t.Run("finds matches and names the document", func(t *testing.T) {
		result := callToolRPC(t, s, "docs__search_docs", `{"query":"ttl unload"}`)
		if result.IsError {
			t.Fatalf("isError = true: %s", result.text())
		}
		if !strings.Contains(result.text(), "guides/routing/unloading") {
			t.Errorf("expected hit missing:\n%s", result.text())
		}
		if !strings.Contains(result.text(), "docs__get_doc") {
			t.Errorf("no pointer to the next call:\n%s", result.text())
		}
	})

	// An empty result set is a valid answer, not an error.
	t.Run("no matches is not an error", func(t *testing.T) {
		result := callToolRPC(t, s, "docs__search_docs", `{"query":"zzzzqqqq"}`)
		if result.IsError {
			t.Error("isError = true, want false for an empty result set")
		}
		if !strings.Contains(result.text(), "No matches") {
			t.Errorf("content = %q", result.text())
		}
	})

	t.Run("missing query", func(t *testing.T) {
		if !callToolRPC(t, s, "docs__search_docs", `{}`).IsError {
			t.Error("isError = false, want true")
		}
	})
}

func TestServer_APIMCP_GetConfigSchema(t *testing.T) {
	s := newMCPServer()

	tests := []struct {
		name    string
		args    string
		wantHas []string
	}{
		{"top level when omitted", `{}`, []string{"(top level)", "models", "globalTTL"}},
		{"a leaf", `{"path":"globalTTL"}`, []string{"type: integer", "Default TTL in seconds.", "default: 0"}},
		{"a map advertises the wildcard", `{"path":"models"}`, []string{`"models.*"`}},
		{"through the wildcard", `{"path":"models.*"}`, []string{"cmd", "(required)", "The command to run."}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callToolRPC(t, s, "docs__get_config_schema", tt.args)
			if result.IsError {
				t.Fatalf("isError = true: %s", result.text())
			}
			for _, want := range tt.wantHas {
				if !strings.Contains(result.text(), want) {
					t.Errorf("content missing %q:\n%s", want, result.text())
				}
			}
		})
	}
}

// Small models frequently stringify numbers. Tolerating that avoids a wasted
// round trip on a purely cosmetic mistake.
func TestServer_APIMCP_TolerantArgumentTypes(t *testing.T) {
	s := newMCPServer()

	if callToolRPC(t, s, "docs__search_docs", `{"query":"ttl","max_results":"3"}`).IsError {
		t.Error("stringified max_results rejected")
	}
	if callToolRPC(t, s, "docs__get_doc", `{"id":"guides/routing/unloading","max_lines":"5"}`).IsError {
		t.Error("stringified max_lines rejected")
	}
}
