package docagent

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestDocs_Search_EmptyQueryReturnsNothing(t *testing.T) {
	d := New(testFS())
	for _, query := range []string{"", "   ", "a", "a b", "..."} {
		if got := d.Search(query, SearchOptions{}); len(got) != 0 {
			t.Errorf("Search(%q) returned %d hits, want 0", query, len(got))
		}
	}
}

func TestDocs_Search_HitsCarryDocIDs(t *testing.T) {
	hits := New(testFS()).Search("alpha", SearchOptions{})
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	for _, hit := range hits {
		if hit.ID == "" {
			t.Error("hit has no doc id")
		}
		if hit.Line < 1 {
			t.Errorf("hit line = %d, want >= 1", hit.Line)
		}
		if hit.Snippet == "" {
			t.Error("hit has no snippet")
		}
	}
}

// A title or tag match is a much stronger signal than a passing mention.
func TestDocs_Search_RanksTitleAndTagMatchesHighest(t *testing.T) {
	fsys := testFS()
	fsys["internal/docagent/kb/guides/routing/passing.md"] = &fstest.MapFile{Data: []byte(`---
title: Something else entirely
summary: Unrelated.
category: guides
---

# Something else entirely

A sentence that happens to mention sample once.
`)}

	hits := New(fsys).Search("sample", SearchOptions{MaxHits: 5})
	if len(hits) < 2 {
		t.Fatalf("got %d hits, want at least 2", len(hits))
	}
	// first-tip carries "sample" as a tag; passing.md only mentions it in prose.
	if hits[0].ID != "guides/routing/first-tip" {
		t.Errorf("top hit = %q, want guides/routing/first-tip; hits=%+v", hits[0].ID, hits)
	}
}

func TestDocs_Search_BoundsHits(t *testing.T) {
	d := New(testFS())

	tests := []struct {
		name    string
		opts    SearchOptions
		wantMax int
	}{
		{"default cap", SearchOptions{}, defaultMaxHits},
		{"explicit cap", SearchOptions{MaxHits: 2}, 2},
		{"negative falls back to the default", SearchOptions{MaxHits: -5}, defaultMaxHits},
		{"cannot exceed the hard cap", SearchOptions{MaxHits: 500}, maxSearchHits},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.Search("the setting a first", tt.opts); len(got) > tt.wantMax {
				t.Errorf("got %d hits, want <= %d", len(got), tt.wantMax)
			}
		})
	}
}

func TestDocs_Search_BoundsSnippetWidth(t *testing.T) {
	fsys := testFS()
	fsys["internal/docagent/kb/guides/routing/wide.md"] = &fstest.MapFile{Data: []byte(
		"---\ntitle: Wide\nsummary: Wide.\ncategory: guides\n---\n\n# Wide\n\nneedle " +
			strings.Repeat("x", 600) + "\n")}

	hits := New(fsys).Search("needle", SearchOptions{})
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	for _, line := range strings.Split(hits[0].Snippet, "\n") {
		if len(line) > maxSnippetLineWidth+3 {
			t.Errorf("snippet line is %d chars, want <= %d", len(line), maxSnippetLineWidth+3)
		}
	}
}

// One dense paragraph must not consume the whole result budget.
func TestDocs_Search_MergesAdjacentHits(t *testing.T) {
	fsys := testFS()
	fsys["internal/docagent/kb/guides/routing/dense.md"] = &fstest.MapFile{Data: []byte(`---
title: Dense
summary: Dense.
category: guides
---

# Dense

needle one
needle two
needle three
needle four
needle five
`)}

	hits := New(fsys).Search("needle", SearchOptions{MaxHits: 10, ContextLines: 2})

	dense := 0
	for _, hit := range hits {
		if hit.ID == "guides/routing/dense" {
			dense++
		}
	}
	if dense == 0 {
		t.Fatal("no hits in the dense document")
	}
	if dense > 2 {
		t.Errorf("got %d hits from one dense block, want them merged into at most 2", dense)
	}
}

func TestDocs_Search_ContextLinesAreBounded(t *testing.T) {
	hits := New(testFS()).Search("alpha", SearchOptions{MaxHits: 1, ContextLines: 500})
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	lines := strings.Count(hits[0].Snippet, "\n") + 1
	if lines > maxContextLines*2+1 {
		t.Errorf("snippet has %d lines, want <= %d", lines, maxContextLines*2+1)
	}
}

func TestDocs_Search_IsDeterministic(t *testing.T) {
	d := New(testFS())
	first := d.Search("alpha setting", SearchOptions{MaxHits: 5})

	for i := 0; i < 5; i++ {
		got := d.Search("alpha setting", SearchOptions{MaxHits: 5})
		if len(got) != len(first) {
			t.Fatalf("run %d returned %d hits, want %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j].ID != first[j].ID || got[j].Line != first[j].Line {
				t.Fatalf("run %d differs at %d: %s:%d vs %s:%d",
					i, j, got[j].ID, got[j].Line, first[j].ID, first[j].Line)
			}
		}
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"TTL Unload", []string{"ttl", "unload"}},
		{"a ttl", []string{"ttl"}},                  // single characters are dropped
		{"ttl, unload.", []string{"ttl", "unload"}}, // punctuation trimmed
		{"ttl ttl", []string{"ttl"}},                // deduplicated
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := tokenize(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("tokenize(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("tokenize(%q) = %v, want %v", tt.in, got, tt.want)
				}
			}
		})
	}
}

func TestIsYAMLKeyLine(t *testing.T) {
	tests := map[string]bool{
		"models:":                    true,
		"ttl: 60":                    true,
		`"quoted-key":`:              true,
		"unloadTimeout: 10":          true,
		"just some prose":            false,
		"prose with: a colon inside": false,
		":leading":                   false,
		"":                           false,
	}
	for in, want := range tests {
		if got := isYAMLKeyLine(in); got != want {
			t.Errorf("isYAMLKeyLine(%q) = %v, want %v", in, got, want)
		}
	}
}
