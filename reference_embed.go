package main

import "embed"

// referenceFiles carries llama-swap's own documentation into the binary so the
// Playground's agentic chat and any MCP client can search and read it through
// the /api/mcp endpoint.
//
// The knowledge base is embedded as a directory: adding an article to
// internal/docagent/db/ must not require editing this directive. Everything else is an
// explicit file list, deliberately never "//go:embed docs" -- docs/ is over
// 3MB, almost all of it images under docs/assets/. docs/newrouter-todo.md is
// also excluded: it is a stale internal TODO, and a stale internal TODO is
// exactly what a small model will confidently repeat as documentation.
//
// There is no build tag here, unlike ui_dist. These files are always present
// in the tree, so plain `go build` and `go test ./...` work unconditionally
// and the feature ships in development builds too.
//
//go:embed internal/docagent/db
//go:embed config.example.yaml config-schema.json README.md
var referenceFiles embed.FS
