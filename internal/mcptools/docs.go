package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/reference"
)

// Tool results are read straight into a model's context window, which for a
// local model is commonly 4k-32k tokens. Every result passes through one of
// these caps.
const (
	// MaxToolResultBytes is exported so transport tests can assert the cap.
	MaxToolResultBytes   = 16 * 1024
	maxToolResultBytes   = MaxToolResultBytes
	maxSearchResultBytes = 8 * 1024
	maxSchemaResultBytes = 8 * 1024

	defaultDocMaxLines = 250

	// MaxDocLines is exported so transport tests can assert the clamp.
	MaxDocLines    = 400
	maxDocMaxLines = MaxDocLines
)

// DocsProvider serves llama-swap's own embedded documentation.
//
// It is the reference implementation of Provider: everything it does is an
// in-memory read of an immutable index, so it holds no resources and its
// Shutdown is a no-op.
type DocsProvider struct {
	docs *reference.Docs
}

// NewDocsProvider builds the provider. A nil or disabled *reference.Docs
// yields a provider that advertises no tools rather than an error, matching
// how the rest of llama-swap treats missing documentation.
func NewDocsProvider(docs *reference.Docs) *DocsProvider {
	return &DocsProvider{docs: docs}
}

func (p *DocsProvider) ID() string { return "docs" }

func (p *DocsProvider) Shutdown(time.Duration) error { return nil }

// toolCategories is the closed set list_docs accepts, matching the knowledge
// base layout plus the synthesized reference docs.
var toolCategories = []string{"tutorials", "guides", "examples", "reference"}

// Reading documentation is both read-only and idempotent: the same call
// returns the same answer until the binary is rebuilt.
func docsAnnotations() *Annotations {
	return &Annotations{ReadOnlyHint: true, IdempotentHint: true}
}

// Tools lists the documentation tools. Names are local; the registry adds the
// "docs" namespace.
func (p *DocsProvider) Tools(context.Context) ([]Tool, error) {
	if !p.docs.Enabled() {
		return nil, nil
	}

	// Constraining `id` to an enum of real doc ids is what stops a small model
	// inventing one. It costs roughly 1KB per request at the current corpus
	// size; past a few hundred docs, drop the enum and rely on list_docs.
	ids := p.docs.IDs()

	return []Tool{
		{
			Name:  "list_docs",
			Title: "List llama-swap documentation",
			Description: "List the available llama-swap documentation. Returns every document's id, title and summary. " +
				"Call this first to see what exists, then call " + QualifyName(p.ID(), "get_doc") + " with an id.",
			Annotations: docsAnnotations(),
			Tags:        []string{"docs", "index", "configuration"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"category": map[string]any{
						"type":        "string",
						"description": "Only list documents in this category.",
						"enum":        toolCategories,
					},
					"tag": map[string]any{
						"type":        "string",
						"description": "Only list documents carrying this tag, for example 'ttl' or 'docker'.",
					},
				},
				"additionalProperties": false,
			},
		},
		{
			Name:  "get_doc",
			Title: "Read a llama-swap document",
			Description: "Read one llama-swap document by id. Ids come from " + QualifyName(p.ID(), "list_docs") + " or " +
				QualifyName(p.ID(), "search_docs") + ". " +
				"Ids starting with 'reference/config/' return that section of config.example.yaml verbatim, comments included.",
			Annotations: docsAnnotations(),
			Tags:        []string{"docs", "configuration"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "The document id, for example 'guides/model-runtime/ttl-and-unloading' or 'reference/config/models'.",
						"enum":        ids,
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Line to start from, for continuing a truncated document. Defaults to 0.",
						"minimum":     0,
					},
					"max_lines": map[string]any{
						"type":        "integer",
						"description": fmt.Sprintf("Maximum lines to return. Defaults to %d, maximum %d.", defaultDocMaxLines, maxDocMaxLines),
						"minimum":     1,
						"maximum":     maxDocMaxLines,
					},
				},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
		},
		{
			Name:  "search_docs",
			Title: "Search llama-swap documentation",
			Description: "Required first step for every llama-swap question. Keyword search across all documentation, including config.example.yaml. " +
				"Returns matching snippets with the document id to read next, via " + QualifyName(p.ID(), "get_doc") + ".",
			Annotations: docsAnnotations(),
			Tags:        []string{"docs", "search", "configuration"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Keywords, for example 'ttl unload' or 'draft model speculative'.",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of matches. Defaults to 5, maximum 15.",
						"minimum":     1,
						"maximum":     15,
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
		{
			Name:  "get_config_schema",
			Title: "Look up a llama-swap config key",
			Description: "Look up the JSON schema for a configuration key: its type, description, default, and direct child keys. " +
				"Always call this when the user names or asks about a configuration key, even if search results already answer the question. " +
				"An unknown-path error proves that key does not exist; say that explicitly before suggesting a real alternative.",
			Annotations: docsAnnotations(),
			Tags:        []string{"docs", "schema", "configuration"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type": "string",
						"description": "Dotted path into the config schema. Use '*' for a map's keys, " +
							"for example 'models.*.filters'. Omit or leave empty for the top level.",
					},
				},
				"additionalProperties": false,
			},
		},
	}, nil
}

// Call runs one documentation tool by its local name.
func (p *DocsProvider) Call(_ context.Context, name string, args map[string]json.RawMessage) (Result, error) {
	if !p.docs.Enabled() {
		return Result{}, fmt.Errorf("no documentation is indexed")
	}

	switch name {
	case "list_docs":
		return p.listDocs(args), nil
	case "get_doc":
		return p.getDoc(args), nil
	case "search_docs":
		return p.searchDocs(args), nil
	case "get_config_schema":
		return p.getConfigSchema(args), nil
	default:
		return Result{}, fmt.Errorf("unknown tool %q", name)
	}
}

func (p *DocsProvider) listDocs(args map[string]json.RawMessage) Result {
	filter := reference.IndexFilter{
		Category: stringArg(args, "category"),
		Tag:      stringArg(args, "tag"),
	}

	index := p.docs.Index(filter)
	if len(index) == 0 {
		return Result{
			IsError: true,
			Content: fmt.Sprintf("No documents matched. Valid categories are: %s. Call %s with no arguments to see everything.",
				strings.Join(toolCategories, ", "), QualifyName(p.ID(), "list_docs")),
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "llama-swap documentation. Read one with %s using its id.\n", QualifyName(p.ID(), "get_doc"))

	category := ""
	for _, meta := range index {
		if meta.Category != category {
			category = meta.Category
			fmt.Fprintf(&b, "\n## %s\n", category)
		}
		fmt.Fprintf(&b, "- %s — %s: %s\n", meta.ID, meta.Title, meta.Summary)
	}

	return capResult(b.String(), maxToolResultBytes)
}

func (p *DocsProvider) getDoc(args map[string]json.RawMessage) Result {
	id := stringArg(args, "id")
	if id == "" {
		return Result{IsError: true, Content: fmt.Sprintf(
			"Error: \"id\" is required. Call %s to see the available ids.", QualifyName(p.ID(), "list_docs"))}
	}

	maxLines := intArg(args, "max_lines", defaultDocMaxLines)
	if maxLines > maxDocMaxLines {
		maxLines = maxDocMaxLines
	}

	doc, ok := p.docs.Doc(id, reference.DocOptions{
		Offset:   intArg(args, "offset", 0),
		MaxLines: maxLines,
		MaxBytes: maxToolResultBytes,
	})
	if !ok {
		return Result{IsError: true, Content: unknownDocMessage(p.ID(), id, p.docs.IDs())}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s)\n", doc.Title, doc.ID)
	fmt.Fprintf(&b, "source: %s lines %d-%d\n\n", doc.Source, doc.SourceLine, doc.SourceLine+(doc.EndLine-doc.StartLine))

	if doc.Fence != "" {
		fmt.Fprintf(&b, "```%s\n%s\n```\n", doc.Fence, doc.Text)
	} else {
		b.WriteString(doc.Text)
		b.WriteString("\n")
	}

	if doc.Truncated {
		fmt.Fprintf(&b, "\n[truncated at line %d of %d — call get_doc again with offset=%d]\n",
			doc.EndLine, doc.Lines, doc.EndLine)
	}

	result := capResult(b.String(), maxToolResultBytes)
	result.Truncated = result.Truncated || doc.Truncated
	return result
}

func (p *DocsProvider) searchDocs(args map[string]json.RawMessage) Result {
	query := stringArg(args, "query")
	if query == "" {
		return Result{IsError: true, Content: `Error: "query" is required.`}
	}

	hits := p.docs.Search(query, reference.SearchOptions{
		MaxHits: intArg(args, "max_results", 0),
	})
	if len(hits) == 0 {
		// An empty result set is a valid answer, not an error.
		return Result{
			Content: fmt.Sprintf("No matches for %q. Try %s, or fewer and broader keywords.", query, QualifyName(p.ID(), "list_docs")),
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d matches for %q. Read a document in full with %s.\n", len(hits), query, QualifyName(p.ID(), "get_doc"))
	for i, hit := range hits {
		fmt.Fprintf(&b, "\n%d. %s (line %d) — %s\n```\n%s\n```\n", i+1, hit.ID, hit.Line, hit.Title, hit.Snippet)
	}

	return capResult(b.String(), maxSearchResultBytes)
}

func (p *DocsProvider) getConfigSchema(args map[string]json.RawMessage) Result {
	path := stringArg(args, "path")

	fragment, ok := p.docs.SchemaFragment(path)
	if !ok {
		parent := path
		if idx := strings.LastIndexByte(parent, '.'); idx > 0 {
			parent = parent[:idx]
		} else {
			parent = ""
		}
		content := fmt.Sprintf("Error: no schema at path %q.", path)
		if siblings := p.docs.SchemaPaths(parent); len(siblings) > 0 {
			content += " Valid paths here: " + strings.Join(siblings, ", ")
		}
		return Result{IsError: true, Content: content}
	}

	displayPath := fragment.Path
	if displayPath == "" {
		displayPath = "(top level)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", displayPath)
	if fragment.Type != "" {
		fmt.Fprintf(&b, "type: %s\n", fragment.Type)
	}
	if len(fragment.Default) > 0 {
		fmt.Fprintf(&b, "default: %s\n", fragment.Default)
	}
	if len(fragment.Enum) > 0 {
		fmt.Fprintf(&b, "allowed values: %s\n", fragment.Enum)
	}
	if len(fragment.Required) > 0 {
		fmt.Fprintf(&b, "required keys: %s\n", strings.Join(fragment.Required, ", "))
	}
	if fragment.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", fragment.Description)
	}
	if fragment.MapKeys {
		fmt.Fprintf(&b, "\nKeys are user defined. Use path \"%s*\" to inspect an entry.\n", dotted(fragment.Path))
	}
	if len(fragment.Properties) > 0 {
		b.WriteString("\nkeys:\n")
		for _, prop := range fragment.Properties {
			marker := ""
			if prop.Required {
				marker = " (required)"
			}
			fmt.Fprintf(&b, "- %s%s: %s — %s\n", prop.Name, marker, prop.Type, prop.Description)
		}
		fmt.Fprintf(&b, "\nInspect one with %s path=\"%s<key>\".\n", QualifyName(p.ID(), "get_config_schema"), dotted(fragment.Path))
	}

	return capResult(b.String(), maxSchemaResultBytes)
}

// unknownDocMessage suggests real ids so the model can correct itself instead
// of guessing again.
func unknownDocMessage(providerID, id string, ids []string) string {
	needle := strings.ToLower(id)
	if idx := strings.LastIndexByte(needle, '/'); idx >= 0 {
		needle = needle[idx+1:]
	}

	var suggestions []string
	for _, candidate := range ids {
		if strings.Contains(strings.ToLower(candidate), needle) {
			suggestions = append(suggestions, candidate)
		}
	}
	if len(suggestions) == 0 {
		suggestions = ids
	}
	sort.Strings(suggestions)
	if len(suggestions) > 10 {
		suggestions = suggestions[:10]
	}

	return fmt.Sprintf("Error: no document with id %q. Did you mean one of: %s? Call %s to see them all.",
		id, strings.Join(suggestions, ", "), QualifyName(providerID, "list_docs"))
}

func capResult(content string, max int) Result {
	capped, truncated := reference.CapText(content, max)
	if truncated {
		capped += "\n[truncated]"
	}
	return Result{Content: capped, Truncated: truncated}
}

// dotted returns the path with a trailing separator, so callers can append a
// key without special-casing the empty root path.
func dotted(path string) string {
	if path == "" {
		return ""
	}
	return path + "."
}

func stringArg(args map[string]json.RawMessage, key string) string {
	raw, ok := args[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		// Small models sometimes send a number or a bare token. Fall back to
		// the raw text rather than silently treating the argument as absent.
		return strings.Trim(strings.TrimSpace(string(raw)), `"`)
	}
	return strings.TrimSpace(value)
}

func intArg(args map[string]json.RawMessage, key string, fallback int) int {
	raw, ok := args[key]
	if !ok {
		return fallback
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		// Tolerate a stringified number, which small models emit often.
		var asString string
		if json.Unmarshal(raw, &asString) == nil {
			if json.Unmarshal([]byte(asString), &value) == nil {
				return value
			}
		}
		return fallback
	}
	return value
}
