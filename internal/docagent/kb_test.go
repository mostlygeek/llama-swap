package docagent

import (
	"testing"
	"testing/fstest"
)

func TestDocs_KB_ParsesFrontmatter(t *testing.T) {
	d := New(testFS())

	got, ok := d.Doc("guides/routing/first-tip", DocOptions{})
	if !ok {
		t.Fatal("Doc() not found")
	}
	if got.Title != "A first tip" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Summary != "Explains alpha and how it behaves." {
		t.Errorf("Summary = %q", got.Summary)
	}
	if got.Category != "guides" {
		t.Errorf("Category = %q", got.Category)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "alpha" {
		t.Errorf("Tags = %v", got.Tags)
	}
	if len(got.ConfigKeys) != 1 || got.ConfigKeys[0] != "alpha" {
		t.Errorf("ConfigKeys = %v", got.ConfigKeys)
	}
	if got.Source != "internal/docagent/kb/guides/routing/first-tip.md" {
		t.Errorf("Source = %q", got.Source)
	}
	// The frontmatter block must not leak into the body.
	if got.Text == "" || got.Text[0] == '-' {
		t.Errorf("body starts with frontmatter: %q", got.Text)
	}
}

func TestDocs_KB_SkipsReadme(t *testing.T) {
	fsys := testFS()
	fsys["internal/docagent/kb/guides/routing/README.md"] = &fstest.MapFile{Data: []byte("# nested guide index\n")}
	d := New(fsys)
	for _, id := range []string{"guides/README", "guides/routing/README", "internal/docagent/kb/README"} {
		if _, ok := d.Doc(id, DocOptions{}); ok {
			t.Errorf("Doc(%q) ok = true, want false", id)
		}
	}
}

// A dropped delimiter or a typo in the frontmatter must degrade into a worse
// index entry, never a missing one.
func TestDocs_KB_ToleratesBadFrontmatter(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantTitle   string
		wantSummary string
	}{
		{
			name:        "no frontmatter at all",
			body:        "# Fallback title\n\nSome prose here.\n",
			wantTitle:   "Fallback title",
			wantSummary: "Some prose here.",
		},
		{
			name:        "unterminated frontmatter",
			body:        "---\ntitle: Never closed\n\n# Real heading\n\nProse.\n",
			wantTitle:   "Real heading",
			wantSummary: "title: Never closed",
		},
		{
			name:        "malformed yaml in the block",
			body:        "---\ntitle: [unclosed\n---\n\n# Heading wins\n\nProse.\n",
			wantTitle:   "Heading wins",
			wantSummary: "Prose.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := testFS()
			fsys["internal/docagent/kb/guides/routing/odd.md"] = &fstest.MapFile{Data: []byte(tt.body)}

			got, ok := New(fsys).Doc("guides/routing/odd", DocOptions{})
			if !ok {
				t.Fatal("Doc() not found, article was dropped from the index")
			}
			if got.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tt.wantTitle)
			}
			if got.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", got.Summary, tt.wantSummary)
			}
			if got.Category != "guides" {
				t.Errorf("Category = %q, want guides (derived from the directory)", got.Category)
			}
		})
	}
}

func TestDocs_KB_SkipsNonMarkdown(t *testing.T) {
	fsys := testFS()
	fsys["internal/docagent/kb/guides/routing/notes.txt"] = &fstest.MapFile{Data: []byte("not markdown")}
	fsys["internal/docagent/kb/guides/routing/diagram.png"] = &fstest.MapFile{Data: []byte("binary")}

	for _, id := range []string{"guides/routing/notes", "guides/routing/notes.txt", "guides/diagram"} {
		if _, ok := New(fsys).Doc(id, DocOptions{}); ok {
			t.Errorf("Doc(%q) ok = true, want false", id)
		}
	}
}

func TestDocs_KB_EmptyArticleIsNotIndexed(t *testing.T) {
	fsys := testFS()
	fsys["internal/docagent/kb/guides/routing/empty.md"] = &fstest.MapFile{Data: []byte("---\ntitle: Nothing\n---\n\n")}

	if _, ok := New(fsys).Doc("guides/routing/empty", DocOptions{}); ok {
		t.Error("Doc() ok = true, want false for an empty article")
	}
}

// README.md opens with a hero image; a summary of "![hero](...)" is useless.
func TestDocs_ExtraMarkdown_SummarySkipsImages(t *testing.T) {
	got, ok := New(testFS()).Doc("reference/readme", DocOptions{})
	if !ok {
		t.Fatal("Doc() not found")
	}
	if got.Summary != "Run models and swap between them." {
		t.Errorf("Summary = %q", got.Summary)
	}
	if got.Category != "reference" {
		t.Errorf("Category = %q, want reference", got.Category)
	}
}

func TestDocs_ExtraMarkdown_MissingFilesAreSkipped(t *testing.T) {
	// The fixture has no docs/configuration.md.
	if _, ok := New(testFS()).Doc("reference/configuration", DocOptions{}); ok {
		t.Error("Doc() ok = true for a file that does not exist")
	}
}

func TestTruncateSummary(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short is untouched", "hello", 20, "hello"},
		{"trims whitespace", "  hello  ", 20, "hello"},
		{"cuts on a word boundary", "aaaa bbbb cccc dddd", 12, "aaaa bbbb..."},
		{"cuts mid-word when there is no late space", "a bbbbbbbbbbbbbbbb", 10, "a bbbbbbbb..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateSummary(tt.in, tt.max); got != tt.want {
				t.Errorf("truncateSummary(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}
