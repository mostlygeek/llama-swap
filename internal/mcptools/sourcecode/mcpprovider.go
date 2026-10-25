// Package sourcecode serves llama-swap's own source code over MCP, so the Help
// page's Docs agent can check what the knowledge base says against the code
// that implements it.
//
// The source is the Go and TypeScript files of the build itself, without
// tests, copied into _src by `make src` and embedded by the embed_src build
// tag (see embed.go). A binary built without the tag has no source, and every
// code__* tool says so.
package sourcecode

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"time"

	"github.com/mostlygeek/llama-swap/internal/mcptools"
)

// DefaultRepoURL is the canonical llama-swap repository, which code__read
// links to.
const DefaultRepoURL = "https://github.com/mostlygeek/llama-swap"

// DefaultRef names the source when the binary is not a tagged release.
const DefaultRef = "main"

// releaseTag matches the version string a release build carries, e.g. "v266".
// A `make` build carries `git describe` output such as "v266-3-gabc123" instead,
// which names a commit that may never have been pushed.
var releaseTag = regexp.MustCompile(`^v\d+$`)

// RefForVersion picks the ref matching a build's version: the release tag for
// a release build, otherwise DefaultRef.
func RefForVersion(version string) string {
	if releaseTag.MatchString(version) {
		return version
	}
	return DefaultRef
}

// notEmbedded is what every code__* tool says when the binary carries no
// source, so the agent can tell the user why.
const notEmbedded = "llama-swap's source code is not embedded in this build: it was built without the " +
	"embed_src build tag, which release builds and `make` builds set. Answer from the docs__* tools " +
	"instead and say the answer was not checked against the code."

// Options configures a Provider.
type Options struct {
	// Source is the source tree to serve, normally Embedded. Nil means the
	// binary carries no source.
	Source fs.FS
	// Ref is the tag or branch the source came from, used in tool
	// descriptions and code__read's GitHub links. Defaults to DefaultRef.
	Ref string
}

// Provider serves llama-swap's source code as the "code" tool namespace. It
// is built once per process and shared across config reloads, which is why
// Shutdown leaves it alone.
type Provider struct {
	ref   string
	index *Index // nil when there is no source
}

var _ mcptools.Provider = (*Provider)(nil)

// NewProvider builds the provider. A Source with no files is treated as no
// source at all.
func NewProvider(opts Options) *Provider {
	if opts.Ref == "" {
		opts.Ref = DefaultRef
	}
	p := &Provider{ref: opts.Ref}
	if opts.Source != nil {
		p.index, _ = LoadIndex(opts.Source)
	}
	return p
}

func (p *Provider) ID() string { return "code" }

// Shutdown is a no-op: the provider outlives the server that registered it.
func (p *Provider) Shutdown(time.Duration) error { return nil }

func codeAnnotations() *mcptools.Annotations {
	// The source is fixed at build time, so the same call gives the same answer.
	return &mcptools.Annotations{ReadOnlyHint: true, IdempotentHint: true}
}

// Tools lists the source code tools. Names are local; the registry adds the
// "code" namespace. They are listed even when the source is not embedded, so
// a call can say why there is nothing to search.
func (p *Provider) Tools(context.Context) ([]mcptools.Tool, error) {
	return p.fileTools(), nil
}

// Call runs one source code tool by its local name.
func (p *Provider) Call(ctx context.Context, name string, args map[string]json.RawMessage) (mcptools.Result, error) {
	switch name {
	case "grep", "read", "find", "ls":
	default:
		return mcptools.Result{}, fmt.Errorf("unknown tool %q", name)
	}
	if p.index == nil {
		return mcptools.Result{IsError: true, Content: notEmbedded}, nil
	}

	switch name {
	case "grep":
		return p.grep(p.index, args), nil
	case "read":
		return p.read(p.index, args), nil
	case "find":
		return p.find(p.index, args), nil
	default:
		return p.ls(p.index, args), nil
	}
}
