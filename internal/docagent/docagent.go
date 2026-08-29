// Package docagent owns the documentation corpus used by the Docs Agent.
package docagent

import "embed"

// DB contains the knowledge-base guides, examples, and tutorials.
//
//go:embed db
var DB embed.FS
