// Package docs embeds the documentation served by the Docs Agent.
package docs

import "embed"

//go:generate go run ./gen

// Files contains the knowledge base, configuration example, and introductory
// documentation. Large assets under docs/assets are deliberately excluded.
//
//go:embed kb config.example.yaml README.md
var Files embed.FS

// ConfigSchema is the configuration JSON schema.
//
// The file people link to stays at the repository root, where go:embed cannot
// reach it from here, so `go generate ./docs` copies it into this directory.
// TestDocs_GeneratedFilesAreCurrent fails when the copy is stale.
//
//go:embed config-schema.json
var ConfigSchema []byte

// AgentPrompt is the Docs Agent system prompt, copied by `go generate ./docs`
// from ui/src/lib/prompts/docsAgent.ts, where it is edited.
//
//go:embed docs-agent-prompt.md
var AgentPrompt string
