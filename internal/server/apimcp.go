package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/mcptools"
)

// mcpProtocolVersion is the only MCP revision this endpoint speaks.
//
// 2026-07-28 is the revision that removed the initialize/initialized handshake
// and the Mcp-Session-Id header, making every request self-contained. That is
// what lets this be a stateless POST route rather than a session manager.
// Clients pinned to an older revision are told so explicitly, via a
// CodeUnsupportedProtocolVersion error naming what is supported.
const mcpProtocolVersion = "2026-07-28"

// Standard MCP headers, introduced in 2026-07-28 so proxies can route without
// parsing the JSON-RPC body.
const (
	headerMCPProtocolVersion = "Mcp-Protocol-Version"
	headerMCPMethod          = "Mcp-Method"
	headerMCPName            = "Mcp-Name"
)

// JSON-RPC 2.0 error codes, plus the MCP-specific version code from SEP-2575.
const (
	codeParseError                 = -32700
	codeInvalidRequest             = -32600
	codeMethodNotFound             = -32601
	codeInvalidParams              = -32602
	codeInternalError              = -32603
	codeUnsupportedProtocolVersion = -32022
)

// maxMCPBodyBytes bounds the request body. MCP tool arguments are a small JSON
// object; anything larger is a mistake or an attack.
const maxMCPBodyBytes = 64 * 1024

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// metaKeyServerInfo is where SEP-2575 carries server identity, since
// server/discover has no dedicated field for it.
const metaKeyServerInfo = "io.modelcontextprotocol/serverInfo"

// discoverResult answers the SEP-2575 server/discover RPC.
//
// This is how a 2026-07-28 client opens a connection: it asks what the server
// supports instead of performing the old initialize handshake, and only falls
// back to initialize if discover fails. Without this a modern client cannot
// connect at all, so it is not optional for a 2026-07-28-only server.
type discoverResult struct {
	SupportedVersions []string        `json:"supportedVersions"`
	Capabilities      mcpCapabilities `json:"capabilities"`
	Instructions      string          `json:"instructions,omitempty"`
	TTLMs             int             `json:"ttlMs"`
	CacheScope        string          `json:"cacheScope"`
	Meta              map[string]any  `json:"_meta,omitempty"`
}

// listToolsResult is the tools/list result, including the cache hints.
//
// A stateless endpoint can never send notifications/tools/list_changed, so
// clients have to re-poll. ttlMs is what tells them how often, which makes it
// load-bearing here rather than decoration.
type listToolsResult struct {
	Tools      []mcptools.Tool `json:"tools"`
	NextCursor string          `json:"nextCursor,omitempty"`
	TTLMs      int             `json:"ttlMs"`
	CacheScope string          `json:"cacheScope"`
}

// toolListTTLMs is short enough that a config reload propagates without anyone
// being told, long enough that a client is not re-listing every turn.
const toolListTTLMs = 60_000

type mcpCapabilities struct {
	Tools *struct{} `json:"tools,omitempty"`
}

type mcpImplementation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

const mcpInstructions = "Tools are namespaced by provider. The docs__* tools answer questions about " +
	"llama-swap's own configuration: call docs__list_docs for an index, docs__search_docs to find " +
	"something by keyword, docs__get_doc to read a document in full, and docs__get_config_schema to " +
	"check what a configuration key accepts. Document ids starting with 'reference/config/' return " +
	"sections of config.example.yaml verbatim. config__get_config returns the configuration this " +
	"server is running right now, with credentials redacted, for advice about the active setup. " +
	"The sys__* tools report facts about the machine llama-swap is running on."

func (s *Server) mcpDiscover() discoverResult {
	// Computed rather than a literal so that unioning an upstream's
	// capabilities later is a change to this function alone.
	var caps mcpCapabilities
	if s.tools.HasTools() {
		caps.Tools = &struct{}{}
	}

	return discoverResult{
		SupportedVersions: []string{mcpProtocolVersion},
		Capabilities:      caps,
		Instructions:      mcpInstructions,
		TTLMs:             toolListTTLMs,
		CacheScope:        "public",
		Meta: map[string]any{
			metaKeyServerInfo: mcpImplementation{Name: "llama-swap", Version: s.build.Version},
		},
	}
}

// callToolParams is the tools/call request shape. Any _meta the client sends
// (io.modelcontextprotocol/clientInfo and friends) is accepted and ignored.
type callToolParams struct {
	Name      string                     `json:"name"`
	Arguments map[string]json.RawMessage `json:"arguments"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callToolResult is the MCP tools/call result. A tool that ran but could not
// answer sets IsError so the model can read the explanation and retry; only
// protocol failures become JSON-RPC errors.
type callToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

func mcpResult(r mcptools.Result) callToolResult {
	return callToolResult{
		Content: []mcpContent{{Type: "text", Text: r.Content}},
		IsError: r.IsError,
	}
}

// handleAPIMCP serves the documentation tools as a stateless MCP server.
//
// The endpoint deliberately does not enforce Accept or Content-Type. The MCP
// Streamable HTTP transport allows a server to stream, and a streaming server
// must require both 'application/json' and 'text/event-stream'; this one never
// streams and always answers application/json, so demanding a header it then
// ignores would only turn working clients away.
func (s *Server) handleAPIMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// RFC 9110 15.5.6: a 405 must say what is allowed.
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxMCPBodyBytes+1))
	if err != nil {
		writeRPCError(w, nullID(), codeParseError, "could not read request body", nil)
		return
	}
	if len(body) > maxMCPBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	if !s.reference.Enabled() {
		// Matches handleAPIPerformance: a disabled subsystem answers with
		// {"enabled": false} rather than an error.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]bool{"enabled": false})
		return
	}

	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		// JSON-RPC batching was removed from MCP in revision 2025-06-18.
		writeRPCError(w, nullID(), codeInvalidRequest, "JSON-RPC batching is not supported", nil)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Method == "" {
		writeRPCError(w, nullID(), codeParseError, "request is not a JSON-RPC message", nil)
		return
	}

	// Version first: a client on an older revision opens with `initialize`, and
	// answering "unsupported protocol version" is far more useful than "method
	// not found".
	if version := r.Header.Get(headerMCPProtocolVersion); version != mcpProtocolVersion {
		data, _ := json.Marshal(map[string]any{
			"supported": []string{mcpProtocolVersion},
			"requested": version,
		})
		writeRPCError(w, req.ID, codeUnsupportedProtocolVersion,
			fmt.Sprintf("unsupported protocol version; this server speaks MCP %s only", mcpProtocolVersion), data)
		return
	}

	if header := r.Header.Get(headerMCPMethod); header == "" {
		writeRPCError(w, req.ID, codeInvalidRequest,
			fmt.Sprintf("missing required %s header", headerMCPMethod), nil)
		return
	} else if header != req.Method {
		writeRPCError(w, req.ID, codeInvalidRequest,
			fmt.Sprintf("header mismatch: %s is %q but the request method is %q", headerMCPMethod, header, req.Method), nil)
		return
	}

	// A notification carries no id and expects no response body.
	if isNotification(req.ID) {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "server/discover":
		writeRPCResult(w, req.ID, s.mcpDiscover())
	case "tools/list":
		s.mcpToolsList(w, r, req)
	case "tools/call":
		s.mcpToolsCall(w, r, req)
	case "ping":
		writeRPCResult(w, req.ID, struct{}{})
	default:
		writeRPCError(w, req.ID, codeMethodNotFound, fmt.Sprintf("unknown method %q", req.Method), nil)
	}
}

func (s *Server) mcpToolsList(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var params struct {
		Cursor string `json:"cursor"`
	}
	if len(req.Params) > 0 {
		// A malformed params object is not worth failing over when the only
		// field is an optional cursor.
		_ = json.Unmarshal(req.Params, &params)
	}

	tools, nextCursor, err := s.tools.Tools(r.Context(), params.Cursor, mcptools.DefaultPageSize)
	if err != nil {
		if errors.Is(err, mcptools.ErrInvalidCursor) {
			writeRPCError(w, req.ID, codeInvalidParams, "invalid cursor", nil)
			return
		}
		writeRPCError(w, req.ID, codeInternalError, fmt.Sprintf("listing tools: %v", err), nil)
		return
	}
	if tools == nil {
		tools = []mcptools.Tool{}
	}

	writeRPCResult(w, req.ID, listToolsResult{
		Tools:      tools,
		NextCursor: nextCursor,
		TTLMs:      toolListTTLMs,
		CacheScope: "public",
	})
}

func (s *Server) mcpToolsCall(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var params callToolParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPCError(w, req.ID, codeInvalidRequest, "params must be an object with a name", nil)
			return
		}
	}
	if params.Name == "" {
		writeRPCError(w, req.ID, codeInvalidRequest, "params.name is required", nil)
		return
	}

	if header := r.Header.Get(headerMCPName); header == "" {
		writeRPCError(w, req.ID, codeInvalidRequest,
			fmt.Sprintf("missing required %s header for tools/call", headerMCPName), nil)
		return
	} else if header != params.Name {
		writeRPCError(w, req.ID, codeInvalidRequest,
			fmt.Sprintf("header mismatch: %s is %q but params.name is %q", headerMCPName, header, params.Name), nil)
		return
	}

	// r.Context() carries client cancellation, which matters once a provider
	// does I/O rather than reading an in-memory index.
	result, found, err := s.tools.Call(r.Context(), params.Name, params.Arguments)
	if !found {
		// An unknown tool is a protocol error, unlike a tool that ran and
		// could not answer.
		writeRPCError(w, req.ID, codeInvalidRequest, fmt.Sprintf("unknown tool %q", params.Name), nil)
		return
	}
	if err != nil {
		writeRPCError(w, req.ID, codeInternalError, err.Error(), nil)
		return
	}

	writeRPCResult(w, req.ID, mcpResult(result))
}

// isNotification reports whether a request omitted its id, per JSON-RPC 2.0.
func isNotification(id json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(id))
	return trimmed == "" || trimmed == "null"
}

func nullID() json.RawMessage { return json.RawMessage("null") }

// JSON-RPC responses, including error responses, are HTTP 200. Only transport
// failures use HTTP status codes.
func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: normalizeID(id), Result: result})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string, data json.RawMessage) {
	writeRPC(w, rpcResponse{
		JSONRPC: "2.0",
		ID:      normalizeID(id),
		Error:   &rpcError{Code: code, Message: message, Data: data},
	})
}

func writeRPC(w http.ResponseWriter, response rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(headerMCPProtocolVersion, mcpProtocolVersion)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// The response has already started; there is nothing useful left to do.
		return
	}
}

func normalizeID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return nullID()
	}
	return id
}
