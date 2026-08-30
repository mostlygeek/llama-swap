// Package docagent owns the documentation corpus and MCP tools used by the
// Docs Agent.
package docagent

import "embed"

// KB contains the knowledge-base guides, examples, and tutorials.
//
//go:embed kb
var KB embed.FS
