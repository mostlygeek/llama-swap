package docagent

import (
	"testing"
	"testing/fstest"
)

// testFS builds a small stand-in for the repository layout. Tests that need a
// different corpus build their own fstest.MapFS.
func testFS() fstest.MapFS {
	return fstest.MapFS{
		"config.example.yaml": &fstest.MapFile{Data: []byte(`# header line one
# header line two

# alpha: the first setting
# - optional, default: 1
alpha: 1

# beta: the second setting
# - optional
beta:
  nested: true
  # commented: this is not a section
  # other: neither is this
`)},
		"config-schema.json": &fstest.MapFile{Data: []byte(`{
  "type": "object",
  "required": ["models"],
  "definitions": {
    "timeouts": {
      "type": "object",
      "description": "Connection timeouts.",
      "properties": {
        "connect": {"type": "integer", "description": "TCP dial timeout.", "default": 30}
      }
    }
  },
  "properties": {
    "alpha": {"type": "integer", "description": "The first setting.", "default": 1},
    "logLevel": {"type": "string", "enum": ["debug", "info"]},
    "models": {
      "type": "object",
      "additionalProperties": {
        "type": "object",
        "required": ["cmd"],
        "properties": {
          "cmd": {"type": "string", "description": "The command to run."},
          "timeouts": {"$ref": "#/definitions/timeouts"},
          "filters": {
            "type": "object",
            "properties": {
              "stripParams": {"type": "string", "description": "Params to remove.\nSecond line."}
            }
          }
        }
      }
    }
  }
}`)},
		"kb/guides/routing/first-tip.md": &fstest.MapFile{Data: []byte(`---
title: A first tip
summary: Explains alpha and how it behaves.
category: guides
tags: [alpha, sample]
config_keys: [alpha]
---

# A first tip

The alpha setting controls the first thing.

Another paragraph mentioning beta.
`)},
		"kb/tutorials/walkthrough.md": &fstest.MapFile{Data: []byte(`---
title: A walkthrough
summary: Start to finish.
category: tutorials
tags: [getting-started]
---

# A walkthrough

Step one.
`)},
		"kb/README.md": &fstest.MapFile{Data: []byte("# contributor guide\n\nnot an article\n")},
		"README.md":    &fstest.MapFile{Data: []byte("# llama-swap\n\n![hero](docs/assets/hero.webp)\n\nRun models and swap between them.\n")},
	}
}

func TestDocs_Disabled_NilFS(t *testing.T) {
	var nilDocs *Docs
	for name, d := range map[string]*Docs{"nil receiver": nilDocs, "nil fs": New(nil)} {
		t.Run(name, func(t *testing.T) {
			if d.Enabled() {
				t.Fatal("Enabled() = true, want false")
			}
			if got := d.IDs(); got != nil {
				t.Errorf("IDs() = %v, want nil", got)
			}
			if got := d.Index(IndexFilter{}); got != nil {
				t.Errorf("Index() = %v, want nil", got)
			}
			if got := d.Search("alpha", SearchOptions{}); got != nil {
				t.Errorf("Search() = %v, want nil", got)
			}
			if _, ok := d.Doc("guides/routing/first-tip", DocOptions{}); ok {
				t.Error("Doc() ok = true, want false")
			}
			if _, ok := d.SchemaFragment("alpha"); ok {
				t.Error("SchemaFragment() ok = true, want false")
			}
		})
	}
}

// A tree without config.example.yaml is not a llama-swap checkout, so nothing
// is indexed rather than half of it.
func TestDocs_Disabled_WithoutConfigExample(t *testing.T) {
	fsys := testFS()
	delete(fsys, "config.example.yaml")
	if New(fsys).Enabled() {
		t.Fatal("Enabled() = true, want false")
	}
}

func TestDocs_IDs_AreSortedAndUnique(t *testing.T) {
	ids := New(testFS()).IDs()
	if len(ids) == 0 {
		t.Fatal("no ids")
	}
	seen := map[string]bool{}
	for i, id := range ids {
		if seen[id] {
			t.Errorf("duplicate id %q", id)
		}
		seen[id] = true
		if i > 0 && ids[i-1] > id {
			t.Errorf("ids not sorted: %q before %q", ids[i-1], id)
		}
	}
	for _, want := range []string{"guides/routing/first-tip", "tutorials/walkthrough", "reference/config/alpha", "reference/readme"} {
		if !seen[want] {
			t.Errorf("missing id %q", want)
		}
	}
}

func TestDocs_Index_OrdersByCategory(t *testing.T) {
	index := New(testFS()).Index(IndexFilter{})

	var categories []string
	for _, meta := range index {
		if len(categories) == 0 || categories[len(categories)-1] != meta.Category {
			categories = append(categories, meta.Category)
		}
	}

	want := []string{"tutorials", "guides", "reference"}
	if len(categories) != len(want) {
		t.Fatalf("category order = %v, want %v", categories, want)
	}
	for i := range want {
		if categories[i] != want[i] {
			t.Fatalf("category order = %v, want %v", categories, want)
		}
	}
}

func TestDocs_Index_Filters(t *testing.T) {
	d := New(testFS())

	tests := []struct {
		name   string
		filter IndexFilter
		wantID string
		wantN  int
	}{
		{"by category", IndexFilter{Category: "guides"}, "guides/routing/first-tip", 1},
		{"category is case insensitive", IndexFilter{Category: "GUIDES"}, "guides/routing/first-tip", 1},
		{"by tag", IndexFilter{Tag: "getting-started"}, "tutorials/walkthrough", 1},
		{"by config key", IndexFilter{ConfigKey: "alpha"}, "", 2}, // the tip plus the config section
		{"no matches", IndexFilter{Category: "nope"}, "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.Index(tt.filter)
			if len(got) != tt.wantN {
				t.Fatalf("Index(%+v) returned %d docs, want %d", tt.filter, len(got), tt.wantN)
			}
			if tt.wantID != "" && got[0].ID != tt.wantID {
				t.Errorf("first id = %q, want %q", got[0].ID, tt.wantID)
			}
		})
	}
}

func TestDocs_Doc_UnknownIDReturnsFalse(t *testing.T) {
	d := New(testFS())
	for _, id := range []string{"", "nope", "guides/nope", "reference/config/nope"} {
		if _, ok := d.Doc(id, DocOptions{}); ok {
			t.Errorf("Doc(%q) ok = true, want false", id)
		}
	}
}

// Ids look like paths but are map keys, never joined to the filesystem. This
// is the whole reason traversal is not a concern here.
func TestDocs_Doc_IDIsNeverJoinedToAPath(t *testing.T) {
	d := New(testFS())
	for _, id := range []string{
		"../../etc/passwd",
		"guides/routing/../../config.example.yaml",
		"kb/guides/routing/first-tip.md",
		"/etc/passwd",
	} {
		if _, ok := d.Doc(id, DocOptions{}); ok {
			t.Errorf("Doc(%q) ok = true, want false", id)
		}
	}
}

func TestDocs_Doc_WindowsAndTruncates(t *testing.T) {
	d := New(testFS())

	full, ok := d.Doc("guides/routing/first-tip", DocOptions{})
	if !ok {
		t.Fatal("Doc() not found")
	}
	if full.Truncated {
		t.Error("full doc reported Truncated")
	}
	if full.StartLine != 1 || full.EndLine != full.Lines {
		t.Errorf("full doc lines = %d..%d, want 1..%d", full.StartLine, full.EndLine, full.Lines)
	}

	t.Run("max lines truncates", func(t *testing.T) {
		got, _ := d.Doc("guides/routing/first-tip", DocOptions{MaxLines: 2})
		if !got.Truncated {
			t.Error("Truncated = false, want true")
		}
		if got.EndLine != 2 {
			t.Errorf("EndLine = %d, want 2", got.EndLine)
		}
	})

	t.Run("offset paginates", func(t *testing.T) {
		got, _ := d.Doc("guides/routing/first-tip", DocOptions{Offset: 2, MaxLines: 2})
		if got.StartLine != 3 {
			t.Errorf("StartLine = %d, want 3", got.StartLine)
		}
		if got.Text == full.Text {
			t.Error("offset returned the whole document")
		}
	})

	t.Run("offset past the end is empty, not an error", func(t *testing.T) {
		got, ok := d.Doc("guides/routing/first-tip", DocOptions{Offset: 9999})
		if !ok {
			t.Fatal("Doc() ok = false")
		}
		if got.Text != "" {
			t.Errorf("Text = %q, want empty", got.Text)
		}
	})

	t.Run("max bytes truncates at a line boundary", func(t *testing.T) {
		got, _ := d.Doc("guides/routing/first-tip", DocOptions{MaxBytes: 12})
		if !got.Truncated {
			t.Error("Truncated = false, want true")
		}
		if len(got.Text) > 12 {
			t.Errorf("len(Text) = %d, want <= 12", len(got.Text))
		}
	})
}

func TestDocs_Doc_CarriesCitation(t *testing.T) {
	d := New(testFS())

	got, ok := d.Doc("reference/config/beta", DocOptions{})
	if !ok {
		t.Fatal("Doc() not found")
	}
	if got.Source != configExampleFile {
		t.Errorf("Source = %q, want %q", got.Source, configExampleFile)
	}
	if got.Fence != "yaml" {
		t.Errorf("Fence = %q, want yaml", got.Fence)
	}
	// "# beta: the second setting" is line 8 of the fixture.
	if got.SourceLine != 8 {
		t.Errorf("SourceLine = %d, want 8", got.SourceLine)
	}
}

func TestCapText(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		max      int
		want     string
		wantCapd bool
	}{
		{"under the cap", "abc\ndef", 100, "abc\ndef", false},
		{"exactly the cap", "abc", 3, "abc", false},
		{"cuts at a newline", "abcd\nefgh\nijkl", 12, "abcd\nefgh", true},
		{"no newline to cut at", "abcdefghij", 4, "abcd", true},
		{"zero max disables", "abcdefghij", 0, "abcdefghij", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, capped := CapText(tt.in, tt.max)
			if got != tt.want || capped != tt.wantCapd {
				t.Errorf("CapText(%q, %d) = (%q, %v), want (%q, %v)", tt.in, tt.max, got, capped, tt.want, tt.wantCapd)
			}
		})
	}
}
