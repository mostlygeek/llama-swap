package reference

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestDocs_Config_ParsesKeysAndLineRanges(t *testing.T) {
	d := New(testFS())

	tests := []struct {
		id            string
		wantSummary   string
		wantSourceLn  int
		wantBodyFirst string
	}{
		{"reference/config/alpha", "the first setting", 4, "# alpha: the first setting"},
		{"reference/config/beta", "the second setting", 8, "# beta: the second setting"},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got, ok := d.Doc(tt.id, DocOptions{})
			if !ok {
				t.Fatalf("Doc(%q) not found", tt.id)
			}
			if got.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", got.Summary, tt.wantSummary)
			}
			if got.SourceLine != tt.wantSourceLn {
				t.Errorf("SourceLine = %d, want %d", got.SourceLine, tt.wantSourceLn)
			}
			first := strings.SplitN(got.Text, "\n", 2)[0]
			if first != tt.wantBodyFirst {
				t.Errorf("first body line = %q, want %q", first, tt.wantBodyFirst)
			}
			if len(got.ConfigKeys) != 1 {
				t.Errorf("ConfigKeys = %v, want one entry", got.ConfigKeys)
			}
		})
	}
}

// The leading comment block is part of the section: it is the documentation.
func TestDocs_Config_CommentBlockIsTheSectionStart(t *testing.T) {
	got, _ := New(testFS()).Doc("reference/config/alpha", DocOptions{})
	if !strings.Contains(got.Text, "# - optional, default: 1") {
		t.Errorf("section body dropped its comment block:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "alpha: 1") {
		t.Errorf("section body dropped the key line:\n%s", got.Text)
	}
}

// Commented-out keys must not become sections. In the real file "# store:" and
// the "# routing:" nested inside the hooks example would otherwise produce a
// phantom section and a duplicate.
func TestDocs_Config_IgnoresCommentedOutKeys(t *testing.T) {
	d := New(testFS())
	for _, id := range []string{"reference/config/commented", "reference/config/other", "reference/config/nested"} {
		if _, ok := d.Doc(id, DocOptions{}); ok {
			t.Errorf("Doc(%q) ok = true: a commented-out or nested key became a section", id)
		}
	}

	// They stay reachable inside their enclosing section.
	got, _ := d.Doc("reference/config/beta", DocOptions{})
	if !strings.Contains(got.Text, "# commented: this is not a section") {
		t.Errorf("commented block was dropped from the enclosing section:\n%s", got.Text)
	}
}

func TestDocs_Config_LastSectionEndsAtEOF(t *testing.T) {
	got, _ := New(testFS()).Doc("reference/config/beta", DocOptions{})
	if !strings.Contains(got.Text, "# other: neither is this") {
		t.Errorf("last section did not run to EOF:\n%s", got.Text)
	}
	if strings.HasSuffix(got.Text, "\n") {
		t.Error("trailing blank lines were not trimmed")
	}
}

func TestDocs_Config_IntroStopsAtFirstSection(t *testing.T) {
	got, ok := New(testFS()).Doc(introDocID, DocOptions{})
	if !ok {
		t.Fatal("intro doc not found")
	}
	if !strings.Contains(got.Text, "# header line one") {
		t.Errorf("intro missing the header:\n%s", got.Text)
	}
	if strings.Contains(got.Text, "alpha") {
		t.Errorf("intro ran into the first section:\n%s", got.Text)
	}
}

func TestDocs_Config_NoIntroWhenFileStartsWithASection(t *testing.T) {
	fsys := testFS()
	fsys["config.example.yaml"] = &fstest.MapFile{Data: []byte("alpha: 1\n")}

	d := New(fsys)
	if _, ok := d.Doc(introDocID, DocOptions{}); ok {
		t.Error("intro doc created for a file with no header")
	}
	if _, ok := d.Doc("reference/config/alpha", DocOptions{}); !ok {
		t.Error("section missing")
	}
}

// A bare "# routing:" opener carries its description on the following lines.
func TestDocs_Config_SummaryScansPastABareKeyComment(t *testing.T) {
	fsys := testFS()
	fsys["config.example.yaml"] = &fstest.MapFile{Data: []byte(`# routing:
# Controls which models run at the same time.
routing:
  router:
    use: group
`)}

	got, ok := New(fsys).Doc("reference/config/routing", DocOptions{})
	if !ok {
		t.Fatal("Doc() not found")
	}
	if got.Summary != "Controls which models run at the same time." {
		t.Errorf("Summary = %q", got.Summary)
	}
}

func TestDocs_Config_SummaryFallsBackWhenUndocumented(t *testing.T) {
	fsys := testFS()
	fsys["config.example.yaml"] = &fstest.MapFile{Data: []byte("undocumented: 1\n")}

	got, _ := New(fsys).Doc("reference/config/undocumented", DocOptions{})
	if got.Summary != "The undocumented configuration section." {
		t.Errorf("Summary = %q", got.Summary)
	}
}
