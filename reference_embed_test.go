package main

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/reference"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A broken embed pattern is otherwise invisible until someone runs a release
// binary and finds the docs missing.
func TestReferenceFS_ContainsExpectedFiles(t *testing.T) {
	for _, name := range []string{
		"config.example.yaml",
		"config-schema.json",
		"README.md",
		"internal/docagent/db/README.md",
	} {
		data, err := referenceFiles.ReadFile(name)
		require.NoErrorf(t, err, "%s is not embedded", name)
		assert.NotEmptyf(t, data, "%s is embedded but empty", name)
	}
}

func TestReferenceFS_ContainsKnowledgeBase(t *testing.T) {
	articles := 0
	categories := map[string]bool{}

	err := fs.WalkDir(referenceFiles, "internal/docagent/db", func(p string, entry fs.DirEntry, err error) error {
		require.NoError(t, err)
		if entry.IsDir() || !strings.HasSuffix(p, ".md") || strings.EqualFold(entry.Name(), "README.md") {
			return nil
		}
		articles++
		rel := strings.TrimPrefix(p, "internal/docagent/db/")
		categories[strings.Split(rel, "/")[0]] = true
		return nil
	})
	require.NoError(t, err)

	assert.Greaterf(t, articles, 0, "no knowledge base articles were embedded")
	for _, want := range []string{"guides", "examples"} {
		assert.Truef(t, categories[want], "no articles embedded from internal/docagent/db/%s", want)
	}
}

// docs/ is over 3MB, nearly all of it images. Embedding the directory wholesale
// would quietly bloat every binary.
func TestReferenceFS_ExcludesAssets(t *testing.T) {
	blocked := []string{".png", ".webp", ".jpg", ".jpeg", ".gif", ".svg"}

	err := fs.WalkDir(referenceFiles, ".", func(p string, entry fs.DirEntry, err error) error {
		require.NoError(t, err)
		if entry.IsDir() {
			return nil
		}
		for _, ext := range blocked {
			assert.NotEqualf(t, ext, strings.ToLower(path.Ext(p)), "binary asset %s was embedded", p)
		}
		return nil
	})
	require.NoError(t, err)
}

// The embedded tree is what the tool endpoints actually serve, so index it the
// same way the server does.
func TestReferenceFS_IndexesSuccessfully(t *testing.T) {
	docs := reference.New(referenceFiles)
	require.True(t, docs.Enabled(), "embedded files did not index")

	index := docs.Index(reference.IndexFilter{})
	assert.Greater(t, len(index), 30, "expected the full documentation corpus")

	for _, id := range []string{
		"reference/config/models",
		"reference/config/globalTTL",
		"reference/readme",
		"guides/configuration/macros",
	} {
		_, ok := docs.Doc(id, reference.DocOptions{})
		assert.Truef(t, ok, "%s is not reachable from the embedded tree", id)
	}

	_, ok := docs.SchemaFragment("models.*.cmd")
	assert.True(t, ok, "config-schema.json did not parse from the embedded tree")
}
