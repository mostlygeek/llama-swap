// Package mcpserver serves an mcptools.Registry over HTTP as a stateless MCP
// server.
//
// It is the transport half of llama-swap's MCP support; the tools themselves
// live in internal/mcptools. llama-swap's /api/mcp and the public docbot
// package both use it, so they speak exactly the same protocol.
package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/mcptools"
)

// ProtocolVersion is the only MCP revision this endpoint speaks.
//
// 2026-07-28 is the revision that removed the initialize/initialized handshake
// and the Mcp-Session-Id header, making every request self-contained. That is
// what lets this be a stateless POST route rather than a session manager.
// Clients pinned to an older revision are told so explicitly, via a
// CodeUnsupportedProtocolVersion error naming what is supported.
const ProtocolVersion = "2026-07-28"

// Standard MCP headers, introduced in 2026-07-28 so proxies can route without
// parsing the JSON-RPC body.
const (
	HeaderProtocolVersion = "Mcp-Protocol-Version"
	HeaderMethod          = "Mcp-Method"
	HeaderName            = "Mcp-Name"
)

// JSON-RPC 2.0 error codes, plus the MCP-specific version code from SEP-2575.
const (
	CodeParseError                 = -32700
	CodeInvalidRequest             = -32600
	CodeMethodNotFound             = -32601
	CodeInvalidParams              = -32602
	CodeInternalError              = -32603
	CodeUnsupportedProtocolVersion = -32022
)

// MaxBodyBytes bounds the request body. MCP tool arguments are a small JSON
// object; anything larger is a mistake or an attack.
const MaxBodyBytes = 64 * 1024

// toolListTTLMs is short enough that a config reload propagates without anyone
// being told, long enough that a client is not re-listing every turn.
const toolListTTLMs = 60_000

// metaKeyServerInfo is where SEP-2575 carries server identity, since
// server/discover has no dedicated field for it.
const metaKeyServerInfo = "io.modelcontextprotocol/serverInfo"

// Info identifies the server in server/discover.
type Info struct {
	Name    string
	Version string
	// Instructions tells a client what the tools are for.
	Instructions string
}

// Handler serves one registry. It holds no per-request state.
type Handler struct {
	tools *mcptools.Registry
	info  Info
}

// New returns a Handler serving tools.
func New(tools *mcptools.Registry, info Info) *Handler {
	return &Handler{tools: tools, info: info}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

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

type mcpCapabilities struct {
	Tools *struct{} `json:"tools,omitempty"`
}

type mcpImplementation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

func (h *Handler) discover() discoverResult {
	// Computed rather than a literal so that unioning an upstream's
	// capabilities later is a change to this function alone.
	var caps mcpCapabilities
	if h.tools.HasTools() {
		caps.Tools = &struct{}{}
	}

	return discoverResult{
		SupportedVersions: []string{ProtocolVersion},
		Capabilities:      caps,
		Instructions:      h.info.Instructions,
		TTLMs:             toolListTTLMs,
		CacheScope:        "public",
		Meta: map[string]any{
			metaKeyServerInfo: mcpImplementation{Name: h.info.Name, Version: h.info.Version},
		},
	}
}

// callToolParams is the tools/call request shape. Any _meta the client sends
// (io.modelcontextprotocol/clientInfo and friends) is accepted and ignored.
type callToolParams struct {
	Name      string                     `json:"name"`
	Arguments map[string]json.RawMessage `json:"arguments"`
}

// Content is one block of a tools/call result.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CallToolResult is the MCP tools/call result. A tool that ran but could not
// answer sets IsError so the model can read the explanation and retry; only
// protocol failures become JSON-RPC errors.
type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

func mcpResult(r mcptools.Result) CallToolResult {
	return CallToolResult{
		Content: []Content{{Type: "text", Text: r.Content}},
		IsError: r.IsError,
	}
}

// ServeHTTP handles one MCP request.
//
// The endpoint deliberately does not enforce Accept or Content-Type. The MCP
// Streamable HTTP transport allows a server to stream, and a streaming server
// must require both 'application/json' and 'text/event-stream'; this one never
// streams and always answers application/json, so demanding a header it then
// ignores would only turn working clients away.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// RFC 9110 15.5.6: a 405 must say what is allowed.
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		writeRPCError(w, nullID(), CodeParseError, "could not read request body", nil)
		return
	}
	if len(body) > MaxBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		// JSON-RPC batching was removed from MCP in revision 2025-06-18.
		writeRPCError(w, nullID(), CodeInvalidRequest, "JSON-RPC batching is not supported", nil)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Method == "" {
		writeRPCError(w, nullID(), CodeParseError, "request is not a JSON-RPC message", nil)
		return
	}

	// Version first: a client on an older revision opens with `initialize`, and
	// answering "unsupported protocol version" is far more useful than "method
	// not found".
	if version := r.Header.Get(HeaderProtocolVersion); version != ProtocolVersion {
		data, _ := json.Marshal(map[string]any{
			"supported": []string{ProtocolVersion},
			"requested": version,
		})
		writeRPCError(w, req.ID, CodeUnsupportedProtocolVersion,
			fmt.Sprintf("unsupported protocol version; this server speaks MCP %s only", ProtocolVersion), data)
		return
	}

	if header := r.Header.Get(HeaderMethod); header == "" {
		writeRPCError(w, req.ID, CodeInvalidRequest,
			fmt.Sprintf("missing required %s header", HeaderMethod), nil)
		return
	} else if header != req.Method {
		writeRPCError(w, req.ID, CodeInvalidRequest,
			fmt.Sprintf("header mismatch: %s is %q but the request method is %q", HeaderMethod, header, req.Method), nil)
		return
	}

	// A notification carries no id and expects no response body.
	if isNotification(req.ID) {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "server/discover":
		writeRPCResult(w, req.ID, h.discover())
	case "tools/list":
		h.toolsList(w, r, req)
	case "tools/call":
		h.toolsCall(w, r, req)
	case "ping":
		writeRPCResult(w, req.ID, struct{}{})
	default:
		writeRPCError(w, req.ID, CodeMethodNotFound, fmt.Sprintf("unknown method %q", req.Method), nil)
	}
}

func (h *Handler) toolsList(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var params struct {
		Cursor string `json:"cursor"`
	}
	if len(req.Params) > 0 {
		// A malformed params object is not worth failing over when the only
		// field is an optional cursor.
		_ = json.Unmarshal(req.Params, &params)
	}

	tools, nextCursor, err := h.tools.Tools(r.Context(), params.Cursor, mcptools.DefaultPageSize)
	if err != nil {
		if errors.Is(err, mcptools.ErrInvalidCursor) {
			writeRPCError(w, req.ID, CodeInvalidParams, "invalid cursor", nil)
			return
		}
		writeRPCError(w, req.ID, CodeInternalError, fmt.Sprintf("listing tools: %v", err), nil)
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

func (h *Handler) toolsCall(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var params callToolParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPCError(w, req.ID, CodeInvalidRequest, "params must be an object with a name", nil)
			return
		}
	}
	if params.Name == "" {
		writeRPCError(w, req.ID, CodeInvalidRequest, "params.name is required", nil)
		return
	}

	if header := r.Header.Get(HeaderName); header == "" {
		writeRPCError(w, req.ID, CodeInvalidRequest,
			fmt.Sprintf("missing required %s header for tools/call", HeaderName), nil)
		return
	} else if header != params.Name {
		writeRPCError(w, req.ID, CodeInvalidRequest,
			fmt.Sprintf("header mismatch: %s is %q but params.name is %q", HeaderName, header, params.Name), nil)
		return
	}

	// r.Context() carries client cancellation, which matters once a provider
	// does I/O rather than reading an in-memory index.
	result, found, err := h.tools.Call(r.Context(), params.Name, params.Arguments)
	if !found {
		// An unknown tool is a protocol error, unlike a tool that ran and
		// could not answer.
		writeRPCError(w, req.ID, CodeInvalidRequest, fmt.Sprintf("unknown tool %q", params.Name), nil)
		return
	}
	if err != nil {
		writeRPCError(w, req.ID, CodeInternalError, err.Error(), nil)
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
		Error:   &RPCError{Code: code, Message: message, Data: data},
	})
}

func writeRPC(w http.ResponseWriter, response rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(HeaderProtocolVersion, ProtocolVersion)
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
