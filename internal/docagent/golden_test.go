package docagent

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// repoFS is the embedded documentation source tree.
func repoFS(t *testing.T) fs.FS {
	t.Helper()
	return os.DirFS("../../docs")
}

func repoDocs(t *testing.T) *Docs {
	t.Helper()
	schema, err := os.ReadFile("../../config-schema.json")
	if err != nil {
		t.Fatalf("read config schema: %v", err)
	}
	return NewWithSchema(repoFS(t), schema)
}

// TestKB_FrontmatterIsValid is what keeps docs/kb honest. Everything the
// contributor guide in docs/kb/README.md promises is enforced here.
func TestKB_FrontmatterIsValid(t *testing.T) {
	fsys := repoFS(t)
	docs := repoDocs(t)

	if !docs.Enabled() {
		t.Fatal("reference library did not index the repository")
	}

	categories := map[string]bool{"guides": true, "examples": true, "tutorials": true}
	seenIDs := map[string]string{}
	articles := 0

	err := fs.WalkDir(fsys, kbDir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(p, ".md") || strings.EqualFold(entry.Name(), "README.md") {
			return nil
		}
		articles++

		t.Run(p, func(t *testing.T) {
			raw, err := fs.ReadFile(fsys, p)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			// Git may check these Markdown files out with CRLF line endings on
			// Windows. Normalize them so this validation checks the document
			// structure rather than the platform's newline convention.
			content := strings.ReplaceAll(string(raw), "\r\n", "\n")

			if !strings.HasPrefix(content, "---\n") {
				t.Fatal("missing frontmatter block; see docs/kb/README.md")
			}

			var fm frontmatter
			block, _, _ := strings.Cut(strings.TrimPrefix(content, "---\n"), "\n---")
			if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
				t.Fatalf("frontmatter is not valid YAML: %v", err)
			}

			if strings.TrimSpace(fm.Title) == "" {
				t.Error("title is required")
			}
			if strings.TrimSpace(fm.Summary) == "" {
				t.Error("summary is required")
			}
			if len(fm.Summary) > maxSummaryLen {
				t.Errorf("summary is %d chars, want <= %d", len(fm.Summary), maxSummaryLen)
			}

			rel := strings.TrimPrefix(p, kbDir+"/")
			if rel == p || rel == "" {
				t.Fatalf("unexpected path outside %s: %s", kbDir, p)
			}
			category := strings.Split(rel, "/")[0]
			if !categories[category] {
				t.Fatalf("article lives in unknown category directory %q", category)
			}
			if fm.Category != category {
				t.Errorf("category = %q, want %q to match the directory", fm.Category, category)
			}

			id := strings.TrimSuffix(rel, ".md")
			if prev, dup := seenIDs[id]; dup {
				t.Errorf("duplicate doc id %q, also produced by %s", id, prev)
			}
			seenIDs[id] = p

			if _, ok := docs.Doc(id, DocOptions{}); !ok {
				t.Errorf("article is not reachable as %q", id)
			}

			for _, tag := range fm.Tags {
				if tag != strings.ToLower(tag) {
					t.Errorf("tag %q should be lowercase", tag)
				}
			}

			// A renamed or removed config key is caught here rather than
			// silently rotting in prose.
			for _, key := range fm.ConfigKeys {
				if _, ok := docs.SchemaFragment(key); !ok {
					t.Errorf("config_keys entry %q does not resolve in %s", key, configSchemaFile)
				}
			}
		})

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", kbDir, err)
	}

	if articles == 0 {
		t.Fatalf("no articles found under %s", kbDir)
	}
}

// TestKB_CrossReferencesResolve catches links to renamed or deleted articles.
func TestKB_CrossReferencesResolve(t *testing.T) {
	docs := repoDocs(t)

	known := map[string]bool{}
	for _, id := range docs.IDs() {
		known[id] = true
	}

	// Backtick-quoted ids like `guides/model-runtime/ttl-and-unloading` are how articles refer
	// to each other.
	for _, meta := range docs.Index(IndexFilter{}) {
		if meta.Category == "reference" {
			continue
		}
		body, _ := docs.Doc(meta.ID, DocOptions{})
		for _, field := range strings.Split(body.Text, "`") {
			if !strings.HasPrefix(field, "guides/") &&
				!strings.HasPrefix(field, "examples/") &&
				!strings.HasPrefix(field, "tutorials/") &&
				!strings.HasPrefix(field, "reference/config/") {
				continue
			}
			// "reference/config/*" is how prose refers to the whole family.
			if strings.ContainsAny(field, " \n") || strings.HasSuffix(field, "*") {
				continue
			}
			if !known[field] {
				t.Errorf("%s references unknown doc id %q", meta.ID, field)
			}
		}
	}
}

func TestDocs_RealConfigExample_SectionKeys(t *testing.T) {
	docs := repoDocs(t)

	// Every top-level key of config.example.yaml, in file order. Update this
	// list deliberately when the reference file gains or loses a section.
	want := []string{
		"healthCheckTimeout", "logLevel", "logTimeFormat", "logToStdout",
		"metricsMaxInMemory", "captureBuffer", "ui", "performance", "startPort",
		"sendLoadingState", "includeAliasesInList", "globalTTL", "unloadTimeout",
		"macros", "apiKeys", "upstream", "profiles", "selectors", "models",
		"hooks", "routing", "peers",
	}

	for _, key := range want {
		id := configDocPrefix + key
		got, ok := docs.Doc(id, DocOptions{})
		if !ok {
			t.Errorf("missing config section doc %q", id)
			continue
		}
		if strings.TrimSpace(got.Summary) == "" {
			t.Errorf("%s has an empty summary", id)
		}
		if got.Source != configExampleFile {
			t.Errorf("%s Source = %q", id, got.Source)
		}
		if got.SourceLine < 1 {
			t.Errorf("%s SourceLine = %d", id, got.SourceLine)
		}
	}

	// Guard against phantom sections. "# store:" and the "# routing:" nested in
	// the hooks example are commented out and must not be indexed as sections.
	sections := 0
	for _, id := range docs.IDs() {
		if strings.HasPrefix(id, configDocPrefix) && id != introDocID {
			sections++
		}
	}
	if sections != len(want) {
		t.Errorf("indexed %d config sections, want %d", sections, len(want))
	}

	if _, ok := docs.Doc(configDocPrefix+"store", DocOptions{}); ok {
		t.Error("the commented-out store: block was indexed as a section")
	}
}

func TestDocs_RealSchema_KnownPaths(t *testing.T) {
	docs := repoDocs(t)

	for _, path := range []string{
		"", "models", "models.*", "models.*.cmd", "models.*.filters",
		"models.*.capabilities", "models.*.capabilities.tools",
		"routing", "routing.router.use", "peers", "peers.*.apiKey",
		"profiles", "selectors", "macros", "apiKeys", "globalTTL",
	} {
		if _, ok := docs.SchemaFragment(path); !ok {
			t.Errorf("SchemaFragment(%q) ok = false, want true", path)
		}
	}
}

// The whole point of the flat namespace is that the model has one getter, so
// every id the index advertises must actually be fetchable.
func TestDocs_RealTree_EveryIndexedIDIsFetchable(t *testing.T) {
	docs := repoDocs(t)

	index := docs.Index(IndexFilter{})
	if len(index) < 30 {
		t.Fatalf("indexed only %d docs, expected the full corpus", len(index))
	}

	for _, meta := range index {
		got, ok := docs.Doc(meta.ID, DocOptions{})
		if !ok {
			t.Errorf("indexed id %q is not fetchable", meta.ID)
			continue
		}
		if strings.TrimSpace(got.Text) == "" {
			t.Errorf("doc %q has an empty body", meta.ID)
		}
		if strings.TrimSpace(meta.Summary) == "" {
			t.Errorf("doc %q has an empty summary", meta.ID)
		}
	}
}
