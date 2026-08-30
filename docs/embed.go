// Package docs embeds the documentation served by the Docs Agent.
package docs

import "embed"

// Files contains the knowledge base, configuration example, and introductory
// documentation. Large assets under docs/assets are deliberately excluded.
//
//go:embed kb config.example.yaml README.md
var Files embed.FS
