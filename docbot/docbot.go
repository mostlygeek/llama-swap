// Package docbot serves llama-swap's documentation, and optionally its source
// code, as MCP tools over HTTP, for programs other than llama-swap.
//
// It is the documentation half of llama-swap's /api/mcp endpoint: the same
// protocol and the same docs__* and code__* tools, without the tools that need
// a running llama-swap (config__get_config, sys__now). The llama-swap.ai
// website uses it to run the Help agent in the browser.
//
//	bot, err := docbot.New(docbot.Options{Name: "llama-swap.ai"})
//	if err != nil { ... }
//	http.Handle("/api/mcp", bot)
//
// The documentation is embedded in this package. The source code is not,
// because go:embed cannot reach outside the package directory. To serve the
// code__* tools, generate an archive at build time with the gensource command
// in this module and pass it as Options.Source:
//
//	//go:generate go tool gensource -o llama-swap-src.tar.gz
//	//go:embed llama-swap-src.tar.gz
//	var srcArchive []byte
//
//	src, err := docbot.SourceFromArchive(srcArchive)
//	bot, err := docbot.New(docbot.Options{Source: src})
//
// gensource reads the source of the llama-swap version in the caller's
// go.mod, so the docs and the code always come from the same commit.
package docbot

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/mostlygeek/llama-swap/docs"
	"github.com/mostlygeek/llama-swap/internal/docagent"
	"github.com/mostlygeek/llama-swap/internal/mcpserver"
	"github.com/mostlygeek/llama-swap/internal/mcptools"
	"github.com/mostlygeek/llama-swap/internal/mcptools/sourcecode"
)

// ModulePath is llama-swap's Go module path.
const ModulePath = "github.com/mostlygeek/llama-swap"

// Options configures a Handler. The zero value serves the docs__* tools only.
type Options struct {
	// Name and Version identify the server to MCP clients.
	// Name defaults to "llama-swap-docbot".
	Name    string
	Version string

	// Source is llama-swap's source tree for the code__* tools, usually from
	// SourceFromArchive. Nil leaves those tools out.
	Source fs.FS

	// SourceRef is the git ref code__read links to on GitHub. It defaults to
	// the llama-swap commit this binary was built with.
	SourceRef string
}

// Handler serves the MCP endpoint. Mount it at any path; it answers POST
// requests only.
type Handler struct {
	mcp    *mcpserver.Handler
	tools  []string
	prompt string
}

// New builds the documentation index and the tool registry.
func New(opts Options) (*Handler, error) {
	if opts.Name == "" {
		opts.Name = "llama-swap-docbot"
	}

	reference := docagent.NewWithSchema(docs.Files, docs.ConfigSchema)
	if !reference.Enabled() {
		return nil, fmt.Errorf("docbot: no documentation is embedded")
	}

	providers := []mcptools.Provider{docagent.NewDocsProvider(reference)}
	instructions := docsInstructions
	if opts.Source != nil {
		ref := opts.SourceRef
		if ref == "" {
			ref = buildRef()
		}
		providers = append(providers, sourcecode.NewProvider(sourcecode.Options{Source: opts.Source, Ref: ref}))
		instructions += " " + codeInstructions
	}

	registry, err := mcptools.New(providers...)
	if err != nil {
		return nil, fmt.Errorf("docbot: %w", err)
	}

	names, err := toolNames(registry)
	if err != nil {
		return nil, fmt.Errorf("docbot: %w", err)
	}

	return &Handler{
		mcp:    mcpserver.New(registry, mcpserver.Info{Name: opts.Name, Version: opts.Version, Instructions: instructions}),
		tools:  names,
		prompt: filterPrompt(docs.AgentPrompt, names),
	}, nil
}

// ServeHTTP answers one MCP request.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mcp.ServeHTTP(w, r)
}

// Tools returns the names of the tools the handler serves, sorted.
func (h *Handler) Tools() []string {
	return append([]string(nil), h.tools...)
}

// SystemPrompt returns the system prompt of llama-swap's Docs Agent, which
// the LFM3-1.7B-HelpAgent model was fine-tuned on, without the lines that
// describe tools this handler does not serve.
func (h *Handler) SystemPrompt() string {
	return h.prompt
}

// SourceFromArchive loads a source archive written by the gensource command.
func SourceFromArchive(data []byte) (fs.FS, error) {
	return sourcecode.ReadArchive(data)
}

const docsInstructions = "Tools are namespaced by provider. The docs__* tools answer questions about " +
	"llama-swap's configuration: call docs__list_docs for an index, docs__search_docs to find " +
	"something by keyword, docs__get_doc to read a document in full, and docs__get_config_schema to " +
	"check what a configuration key accepts. Document ids starting with 'reference/config/' return " +
	"sections of config.example.yaml verbatim."

const codeInstructions = "The code__* tools search and read llama-swap's source code, to check the " +
	"documentation against the implementation: code__grep for a regex search, code__read to read a " +
	"file, code__find to find files by glob, and code__ls to list a directory."

func toolNames(registry *mcptools.Registry) ([]string, error) {
	var names []string
	cursor := ""
	for {
		page, next, err := registry.Tools(context.Background(), cursor, mcptools.DefaultPageSize)
		if err != nil {
			return nil, err
		}
		for _, tool := range page {
			names = append(names, tool.Name)
		}
		if next == "" {
			sort.Strings(names)
			return names, nil
		}
		cursor = next
	}
}

// listItem matches a bullet ("- ") or numbered ("6. ") line, capturing the
// number when there is one. Every tool description and every rule in the
// Docs Agent prompt is one such line.
var listItem = regexp.MustCompile(`^(?:- |(\d+)\. )`)

// toolRef matches a tool name in backticks, such as `code__grep`.
var toolRef = regexp.MustCompile("`([A-Za-z0-9_-]+__[A-Za-z0-9_-]+)`")

// filterPrompt drops every list item that names a tool not in served: the
// tool's own bullet, and any rule telling the model to call it. Describing a
// tool the model cannot call invites it to call one anyway. Numbered rules
// are renumbered so the list stays consecutive.
func filterPrompt(prompt string, served []string) string {
	available := map[string]bool{}
	for _, name := range served {
		available[name] = true
	}

	lines := strings.Split(prompt, "\n")
	kept := lines[:0]
	next := 0 // the next rule number, or 0 outside a numbered list
	for _, line := range lines {
		item := listItem.FindStringSubmatch(line)
		if item != nil && mentionsMissingTool(line, available) {
			continue
		}
		if item != nil && item[1] != "" {
			if item[1] == "1" {
				next = 1
			}
			if next > 0 {
				line = strconv.Itoa(next) + ". " + strings.TrimPrefix(line, item[0])
				next++
			}
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func mentionsMissingTool(line string, available map[string]bool) bool {
	for _, m := range toolRef.FindAllStringSubmatch(line, -1) {
		if !available[m[1]] {
			return true
		}
	}
	return false
}

// pseudoRev matches the commit at the end of a pseudo-version such as
// v0.0.0-20261106234400-e77fe59e642b. llama-swap's release tags (v270) are
// not semantic versions, so Go always records one of these.
var pseudoRev = regexp.MustCompile(`-([0-9a-f]{12})$`)

// buildRef returns the llama-swap commit this binary was built with, for
// GitHub links, or sourcecode.DefaultRef when it cannot tell, such as under a
// replace directive.
func buildRef() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return sourcecode.DefaultRef
	}
	for _, dep := range info.Deps {
		if dep.Path != ModulePath || dep.Replace != nil {
			continue
		}
		if m := pseudoRev.FindStringSubmatch(dep.Version); m != nil {
			return m[1]
		}
		return sourcecode.RefForVersion(dep.Version)
	}
	return sourcecode.DefaultRef
}
