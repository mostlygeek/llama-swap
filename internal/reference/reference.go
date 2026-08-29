// Package reference indexes llama-swap's own documentation so it can be
// served to an LLM through the /api/mcp endpoint.
//
// Everything retrievable is a "doc" with an id in one flat namespace:
//
//	guides/model-runtime/ttl-and-unloading  a knowledge base article from internal/docagent/db/
//	reference/config/models     a top-level section of config.example.yaml
//	reference/readme            README.md
//
// One namespace means the agent needs one index listing, one getter and one
// search rather than a set per source, which materially improves how reliably
// small local models can use the tools.
package reference

import (
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
)

// File names read from the FS. Paths are relative to the FS root, which
// mirrors the repository root.
const (
	configExampleFile = "config.example.yaml"
	configSchemaFile  = "config-schema.json"
	kbDir             = "internal/docagent/db"
)

// Category values. Index listings are ordered by this sequence so a model
// reading top to bottom sees walkthroughs before raw reference material.
var categoryOrder = []string{"tutorials", "guides", "examples", "reference"}

// DocMeta is the index entry for a doc. It is what list_docs returns.
type DocMeta struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Category   string   `json:"category"`
	Tags       []string `json:"tags,omitempty"`
	ConfigKeys []string `json:"config_keys,omitempty"`
	Lines      int      `json:"lines"`
}

// DocText is a doc's body, possibly a windowed slice of it.
type DocText struct {
	DocMeta
	Text       string `json:"text"`
	StartLine  int    `json:"start_line"` // 1-based, within the doc body
	EndLine    int    `json:"end_line"`
	Truncated  bool   `json:"truncated"`
	Source     string `json:"source"`      // file the text came from
	SourceLine int    `json:"source_line"` // line in Source holding body line 1
	Fence      string `json:"fence,omitempty"`
}

// DocOptions windows a doc body. The zero value returns the whole doc subject
// to the default caps applied by the caller.
type DocOptions struct {
	Offset   int // 0-based body line to start at
	MaxLines int // 0 means no line limit
	MaxBytes int // 0 means no byte limit
}

// IndexFilter narrows an index listing. Empty fields do not filter.
type IndexFilter struct {
	Category  string
	Tag       string
	ConfigKey string
}

// doc is the internal representation. Bodies are kept as lines because every
// operation here is line oriented: windowing, search context, and citations.
type doc struct {
	meta       DocMeta
	lines      []string
	source     string
	sourceLine int    // line in source holding lines[0]
	fence      string // non-empty renders the body inside a fenced block
}

// Docs is an immutable index. It is safe for concurrent use and safe to share
// across Server instances, including across a hot config reload.
type Docs struct {
	byID    map[string]*doc
	ordered []*doc
	ids     []string

	schema     map[string]any
	schemaDefs map[string]any
}

// New indexes fsys. It never fails: a nil fsys, or one without
// config.example.yaml, yields a disabled *Docs whose methods return empty
// results. Callers check Enabled and report the feature as unavailable.
func New(fsys fs.FS) *Docs {
	if fsys == nil {
		return &Docs{}
	}

	example, err := fs.ReadFile(fsys, configExampleFile)
	if err != nil {
		return &Docs{}
	}

	d := &Docs{byID: map[string]*doc{}}

	// Order matters: it is the order Index reports within a category, and the
	// order categoryOrder then groups.
	for _, doc := range parseKB(fsys) {
		d.add(doc)
	}
	for _, doc := range parseConfigExample(string(example)) {
		d.add(doc)
	}
	for _, doc := range parseExtraMarkdown(fsys) {
		d.add(doc)
	}

	if raw, err := fs.ReadFile(fsys, configSchemaFile); err == nil {
		var schema map[string]any
		if json.Unmarshal(raw, &schema) == nil {
			d.schema = schema
			if defs, ok := schema["definitions"].(map[string]any); ok {
				d.schemaDefs = defs
			}
		}
	}

	sort.Slice(d.ordered, func(i, j int) bool {
		ci := categoryRank(d.ordered[i].meta.Category)
		cj := categoryRank(d.ordered[j].meta.Category)
		if ci != cj {
			return ci < cj
		}
		return d.ordered[i].meta.ID < d.ordered[j].meta.ID
	})

	d.ids = make([]string, 0, len(d.byID))
	for id := range d.byID {
		d.ids = append(d.ids, id)
	}
	sort.Strings(d.ids)

	return d
}

func (d *Docs) add(doc *doc) {
	if doc == nil || doc.meta.ID == "" {
		return
	}
	if _, exists := d.byID[doc.meta.ID]; exists {
		return // first definition wins; the golden test catches duplicates
	}
	doc.meta.Lines = len(doc.lines)
	d.byID[doc.meta.ID] = doc
	d.ordered = append(d.ordered, doc)
}

func categoryRank(category string) int {
	for i, c := range categoryOrder {
		if c == category {
			return i
		}
	}
	return len(categoryOrder)
}

// Enabled reports whether any documentation was indexed. It is nil-receiver
// safe so a Server built without a reference library works unchanged.
func (d *Docs) Enabled() bool {
	return d != nil && len(d.byID) > 0
}

// IDs returns every doc id, sorted. Used to build the get_doc `id` enum and to
// suggest alternatives when a lookup misses.
func (d *Docs) IDs() []string {
	if !d.Enabled() {
		return nil
	}
	out := make([]string, len(d.ids))
	copy(out, d.ids)
	return out
}

// Index returns matching docs in category order. A zero filter returns
// everything.
func (d *Docs) Index(filter IndexFilter) []DocMeta {
	if !d.Enabled() {
		return nil
	}
	out := make([]DocMeta, 0, len(d.ordered))
	for _, doc := range d.ordered {
		if filter.Category != "" && !strings.EqualFold(doc.meta.Category, filter.Category) {
			continue
		}
		if filter.Tag != "" && !containsFold(doc.meta.Tags, filter.Tag) {
			continue
		}
		if filter.ConfigKey != "" && !containsFold(doc.meta.ConfigKeys, filter.ConfigKey) {
			continue
		}
		out = append(out, doc.meta)
	}
	return out
}

// Doc returns a doc body, windowed by opts. Ids are looked up in a map and are
// never joined to a filesystem path, so an id shaped like "../../etc/passwd"
// is simply a miss.
func (d *Docs) Doc(id string, opts DocOptions) (DocText, bool) {
	if !d.Enabled() {
		return DocText{}, false
	}
	doc, ok := d.byID[strings.TrimSpace(id)]
	if !ok {
		return DocText{}, false
	}

	start := opts.Offset
	if start < 0 {
		start = 0
	}
	if start > len(doc.lines) {
		start = len(doc.lines)
	}

	end := len(doc.lines)
	truncated := false
	if opts.MaxLines > 0 && start+opts.MaxLines < end {
		end = start + opts.MaxLines
		truncated = true
	}

	body := strings.Join(doc.lines[start:end], "\n")
	if opts.MaxBytes > 0 {
		capped, wasCapped := CapText(body, opts.MaxBytes)
		if wasCapped {
			// Recount so EndLine still points at real content.
			end = start + strings.Count(capped, "\n") + 1
			truncated = true
			body = capped
		}
	}

	return DocText{
		DocMeta:    doc.meta,
		Text:       body,
		StartLine:  start + 1,
		EndLine:    end,
		Truncated:  truncated,
		Source:     doc.source,
		SourceLine: doc.sourceLine + start,
		Fence:      doc.fence,
	}, true
}

// CapText truncates s at the last newline before maxBytes. It reports whether
// anything was removed. Tool results land in a local model's context window,
// so every path that produces one passes through here.
func CapText(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s, false
	}
	cut := s[:maxBytes]
	if idx := strings.LastIndexByte(cut, '\n'); idx > 0 {
		cut = cut[:idx]
	}
	return cut, true
}

func containsFold(haystack []string, needle string) bool {
	for _, v := range haystack {
		if strings.EqualFold(v, needle) {
			return true
		}
	}
	return false
}

// truncateSummary keeps index listings compact. Summaries are already required
// to be short; this guards the synthesized ones.
func truncateSummary(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if idx := strings.LastIndexByte(cut, ' '); idx > max/2 {
		cut = cut[:idx]
	}
	return cut + "..."
}
