package server

import (
	"encoding/json"
	"net/http"

	"github.com/mostlygeek/llama-swap/internal/mcpserver"
)

// mcpInstructions is returned from server/discover. The protocol itself lives
// in internal/mcpserver.
const mcpInstructions = "Tools are namespaced by provider. The docs__* tools answer questions about " +
	"llama-swap's own configuration: call docs__list_docs for an index, docs__search_docs to find " +
	"something by keyword, docs__get_doc to read a document in full, and docs__get_config_schema to " +
	"check what a configuration key accepts. Document ids starting with 'reference/config/' return " +
	"sections of config.example.yaml verbatim. config__get_config answers a jq query against the " +
	"configuration this server is running right now, with credentials redacted, for advice about " +
	"the active setup: pass a \"query\" such as \".models | keys\" or \".models.qwen3\" to read " +
	"exactly the part you need. " +
	"The sys__* tools report facts about the machine llama-swap is running on. " +
	"The code__* tools search and read llama-swap's source code at the version this server runs, " +
	"to check the documentation against the implementation: code__grep for a regex search, code__read " +
	"to read a file, code__find to find files by glob, and code__ls to list a directory. The source is " +
	"the Go and TypeScript files without tests, embedded in builds made with the embed_src build tag."

// handleAPIMCP serves the MCP tools at /api/mcp.
func (s *Server) handleAPIMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// RFC 9110 15.5.6: a 405 must say what is allowed.
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.reference.Enabled() {
		// Matches handleAPIPerformance: a disabled subsystem answers with
		// {"enabled": false} rather than an error.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]bool{"enabled": false})
		return
	}

	// The handler is cheap to build and reads s.tools at request time.
	mcpserver.New(s.tools, mcpserver.Info{
		Name:         "llama-swap",
		Version:      s.build.Version,
		Instructions: mcpInstructions,
	}).ServeHTTP(w, r)
}
